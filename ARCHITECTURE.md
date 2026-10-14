# Arquitetura

Este documento registra as decisões de projeto do serviço de apostas da
Jungle Gaming. A estrutura espelha o `docs/CHALLENGE.md` §15 — uma seção
para cada preocupação que o enunciado pede que seja justificada.

---

## 1. Forma geral do sistema

O projeto segue o layout **hexagonal (ports & adapters)**:

```
             HTTP           SQS                 SQS
         (handlers)     (consumer)         (publisher)
              \             |                   /
               \            v                  /
                v    +---- service ----+      /
                     |                 |     /
                     |   casos de uso  |----+---> tabela outbox
                     |   + portas      |    |
                     +-----------------+    |
                              ^             |
                              |             v
                      +---- postgres ----+
                      | wallet, ledger,  |
                      | tx, inbox, outbox|
                      +------------------+
```

- O **domínio** (`internal/domain/*`) não tem imports de infra — compila
  sem `pgx`, `net/http`, SQS ou Fx.
- O pacote **service** é dono das portas (`WalletRepository`,
  `TransactionRepository`, `OutboxRepository`, `InboxRepository`,
  `TxManager`); o adapter **postgres** as implementa.
- O **transporte HTTP**, o **consumidor SQS** e o **publisher da outbox**
  conversam com a mesma superfície do service.
- O Fx é a raiz de composição em `cmd/api/main.go`. Hooks de lifecycle
  (`OnStart`/`OnStop`) tratam o shutdown gracioso.

---

## 2. Money

**Representação:** `int64` da menor unidade da moeda (centavos) mais uma
string ISO-4217. Nenhum float jamais encosta em dinheiro — nem no parsing,
nem na aritmética, nem na serialização, nem na persistência.

**Por que `int64` e não uma biblioteca decimal:** `int64` guarda até
cerca de 92 quatrilhões de centavos (~US$ 920 trilhões), muito acima do
que qualquer serviço de apostas vai ver na prática. Aritmética é O(1) e
overflow é detectável com checagens triviais. Uma biblioteca decimal
adicionaria peso de dependência sem benefício real.

**Parsing e serialização:**

- O contrato externo usa `{"amount":"25.00","currency":"BRL"}` (strings,
  exatamente duas casas decimais, sem notação científica). A regex
  `^(0|[1-9][0-9]*)\.[0-9]{2}$` garante isso na fronteira. O enunciado
  proíbe arredondamento silencioso — este parser rejeita qualquer coisa
  fora do padrão.
- `MarshalJSON` sempre devolve a string com duas casas.

**Overflow:** todo método aritmético devolve erro em overflow (`Add`,
`Subtract`, `Negate`). Zero não é caso especial — é `money.Zero(currency)`.

**Compatibilidade de moeda:** operações entre duas moedas devolvem
`ErrCurrencyMismatch` em vez de reinterpretar valores silenciosamente.

**Persistência:** coluna `BIGINT` + uma coluna `CHAR(3)` para moeda.
Round-trips são lossless.

**Limites documentados:** a regex rejeita valores com mais de 18 dígitos;
a checagem em dois passos no `Parse` (parte inteira × 100, depois +
centavos) captura casos próximos de `MaxInt64`.

---

## 3. Acesso ao banco

**Biblioteca:** `jackc/pgx/v5` + `pgxpool`. Escolhida porque:

- Driver nativo de Postgres com a maior cobertura de features (bulk copy,
  LISTEN/NOTIFY, scan tipado).
- Transações explícitas via `tx.Exec`/`tx.Query` — sem mágica.
- `pgconn.PgError.Code` expõe `SQLSTATE 23505`, então traduzimos
  unique-violation para `service.ErrWalletAlreadyExists` de forma clara.

**Fronteira de transação:** uma porta **`TxManager`** dedicada vive em
`service/ports.go`. Seu único método, `RunInTx(ctx, func(pgx.Tx)
error)`, é implementado por `postgres.TxRunner`. O service chama
`RunInTx` e passa o `pgx.Tx` para todas as chamadas de repositório que
precisam estar na mesma transação. Isso nos dá:

- Um `BEGIN … COMMIT` por caso de uso.
- Um único arquivo (`tx_runner.go`) que faz commit em caso de erro nulo
  e rollback caso contrário.
- Repositórios expõem métodos tanto no nível do pool (`GetWalletByID`)
  quanto no nível da tx (`GetWalletForUpdateTx`) — o chamador escolhe
  leitura-vs-mutação por intenção.

**Mapeamento de Money:** `BIGINT` para o valor, `CHAR(3)` para moeda.
`CHECK (balance_amount >= 0)` garante não-negatividade no nível do
schema — a linha simplesmente não pode existir com saldo negativo mesmo
que a aplicação tenha bug.

---

## 4. Idempotência

Três camadas de defesa, do mais granular para o mais abrangente:

1. **Header HTTP** `Idempotency-Key: provider-a:transaction-123` é
   obrigatório em todo `POST /wagering/transactions`. O handler nunca
   substitui silenciosamente a chave do cliente por uma calculada no
   servidor.
2. **Unicidade no banco** — duas constraints em `wager_transactions`:
   `UNIQUE (provider_id, idempotency_key)` **e**
   `UNIQUE (provider_id, external_transaction_id)`. A primeira pega
   "mesma chave, operação diferente"; a segunda pega "mesma operação,
   chave diferente".
3. **Hash do payload** armazenado na linha da wager. No replay, o
   service compara `PayloadHash(cmd)` com o hash guardado; igual →
   devolve o resultado persistido; diferente → `ErrIdempotencyConflict`
   (HTTP 409).

**Algoritmo do hash:** SHA-256 de um struct cujos campos estão
declarados exatamente na ordem dos campos de negócio — o `encoding/json`
preserva essa ordem. Metadados de transporte (correlation id,
timestamps, headers) ficam de fora. O valor é hasheado como `int64 de
centavos` para evitar ambiguidades de normalização de string.

**Paridade com o SQS:** o consumidor usa `data.idempotencyKey` do
envelope da mensagem como chave de idempotência, então uma requisição
HTTP e uma mensagem SQS que apontam para a mesma operação colidem na
mesma unique constraint. Além disso, a **tabela inbox** oferece
deduplicação por consumer baseada em `messageId` — ver §7.

---

## 5. Concorrência e locks

**Estratégia:** locking pessimista no nível de linha no agregado wallet.
Toda operação que move dinheiro dentro de `processInTx` abre com:

```sql
SELECT ... FROM wallets WHERE id = $1 FOR UPDATE
```

Dois writers concorrentes contra a mesma wallet serializam na linha;
wallets diferentes seguem em paralelo. O enunciado exige isso (§8:
"coordenação deve ocorrer por carteira"; "carteiras diferentes devem
continuar sendo processadas em paralelo").

**Por que pessimista e não otimista:**

- A carga _esperada_ tem alta contenção por wallet (mesmo jogador, BETs
  rápidas de uma sessão de jogo). Otimista + retry queimaria CPU em
  retries.
- O banco impõe `CHECK (balance_amount >= 0)`, então um bug na aplicação
  que esquecesse o lock ainda seria pego pelo schema — defesa em
  profundidade.
- O contador `Wallet.Version` continua sendo incrementado em toda
  mudança de saldo, então um guard _adicional_ otimista
  `UPDATE WHERE version=$old` pode ser plugado depois sem quebrar nada.

**Sem locks globais:** os únicos locks são `FOR UPDATE` na linha
específica da wallet e `FOR UPDATE SKIP LOCKED` em linhas individuais da
outbox. Nada serializa entre wallets.

**Comprovado por teste:** `TestConcurrentBets_100_vs_2x80_OneWinsOneRejects`
e `TestParallel50SameBet_OneDebit` rodam com `-race` e demonstram as
invariantes obrigatórias.

---

## 6. Referências pendentes e regras de reversão

**Gatilho:** um `REFUND` ou `ROLLBACK` chega antes da operação que
referencia.

**Resposta:** persistir a operação como `PENDING_REFERENCE` com
`next_attempt_at` agendado por `BaseBackoff * 2^attempts` (teto em
`MaxBackoff = 1h`), e emitir `WagerTransactionPendingReference`.

**Worker do resolver** (`internal/resolver`) consulta o Postgres pela
`PENDING_REFERENCE` mais antiga cujo `next_attempt_at` está vencido, sob
`FOR UPDATE SKIP LOCKED` para que múltiplas instâncias rodem em
paralelo. O worker:

- Busca a referência por `(providerId, referenceExternalTransactionId)`.
- Valida que provider/player/wallet/currency/round batem.
- Verifica que a ref ainda não foi revertida pelo mesmo tipo.
- Aplica o movimento inverso; emite `WagerTransactionProcessed` +
  `WalletBalanceChanged`.
- Se a referência continua pendente, incrementa `attempts` e reagenda;
  em falha terminal ou TTL esgotado, finaliza como `REJECTED` com o
  código de falha apropriado.

**TTL / máximo de tentativas:** `MaxAttempts = 10`, teto de backoff `1h`.
Esgotado, a operação finaliza em `REJECTED` com
`FailureReferenceNotFound`.

**Status terminais da referência:**

| Status da referência            | Desfecho da reversão                  |
| ------------------------------- | ------------------------------------- |
| `PROCESSED`                     | aplica o inverso                      |
| `PENDING` / `PENDING_REFERENCE` | reagenda                              |
| `REJECTED` / `FAILED`           | rejeita com `REFERENCE_NOT_PROCESSED` |

**Guard de dupla reversão:** `GetProcessedReversalTx` busca qualquer
linha em `PROCESSED` do tipo `REFUND` ou `ROLLBACK` já apontando para a
referência. Se encontrar, a segunda reversão é rejeitada com
`FailureReferenceAlreadyReversed`.

**Saldo insuficiente em reversão:** um `ROLLBACK` que levaria o saldo
abaixo de zero é rejeitado com `FailureRollbackInsufficientFunds` — um
código distinto do `INSUFFICIENT_FUNDS` normal, então dashboards
conseguem diferenciar.

---

## 7. Inbox / outbox

**Por que ambos:** brokers _at-least-once_ podem entregar a mesma
mensagem duas vezes; nosso serviço pode crashar entre commitar a tx no
DB e ackar o broker. Inbox torna duplicatas de entrada idempotentes;
outbox torna a publicação de saída confiável mesmo diante de crashes.

### Outbox

Schema (`outbox`): `event_id (PK)`, `aggregate_id`, `event_type`,
`payload JSONB`, `occurred_at`, `attempts`, `next_delivery_at`,
`published_at`.

**Caminho de escrita** — o service, dentro do callback do seu
`RunInTx`, chama `InsertEventTx` para cada evento emitido. O payload é
o envelope JSON completo (`eventId`, `eventType`, `aggregateId`,
`correlationId`, `occurredAt`, `version`, `data`) para que o publisher
nunca tenha que remontar nada.

**Publisher** (`internal/workers/outboxpublisher`):

```sql
SELECT ... FROM outbox
WHERE published_at IS NULL AND next_delivery_at <= $now
ORDER BY next_delivery_at, event_id
LIMIT $batch
FOR UPDATE SKIP LOCKED
```

Múltiplas instâncias do publisher compartilham isso de forma limpa
porque `SKIP LOCKED` entrega a cada worker uma fatia disjunta. Em falha
de publicação, o worker faz `MarkRetryTx` bumpando `attempts` e
reagendando por `BaseBackoff << attempts` (com teto). Em sucesso, faz
`MarkPublishedTx`.

**Segurança contra crash:** a chamada de publicação para o SQS fica
**dentro** da transação do banco. Quatro casos:

| Estado antes do crash      | Na recuperação                                                                                                                          |
| -------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- |
| Pré-publicação             | Linha ainda não publicada; outro publisher pega                                                                                         |
| Pós-publicação, pré-COMMIT | DB faz rollback; outro publisher republica com o **mesmo `eventId`** → `MessageDeduplicationId=eventId` do SQS FIFO suprime a duplicata |
| Pós-COMMIT                 | Linha com `published_at IS NOT NULL`; nada a fazer                                                                                      |

É assim que a exigência "republicação preserva o `eventId`" do §11 do
enunciado é cumprida sem estado adicional.

### Inbox

Schema (`inbox`): PK `(consumer_name, message_id)`, `message_hash`,
`transaction_id`, `received_at`, `completed_at`.

`MarkReceivedTx` faz `INSERT ... ON CONFLICT DO NOTHING`:

- `inserted = true` → mensagem nova; segue com o trabalho de negócio.
- `inserted = false, prior.hash == incoming` → replay; a tx de negócio
  é detectada como idempotente via `(provider_id, idempotency_key)` e
  devolve o resultado armazenado.
- `inserted = false, prior.hash != incoming` → **poison**;
  `ErrIdempotencyConflict` sobe, o consumer apaga a mensagem no SQS
  (o broker não corrige conteúdo malformado).

**Um único commit:** a escrita na inbox, o mark-completed da inbox, a
mudança de estado do negócio, o lançamento no ledger e os eventos da
outbox vivem todos no mesmo `BEGIN ... COMMIT`.

---

## 8. Autenticação e autorização

**IdP:** Keycloak, provisionado por `deploy/keycloak/wager-realm.json`.
Três clients, todos apenas `client_credentials`:

| Client             | Secret                    | Role de realm |
| ------------------ | ------------------------- | ------------- |
| `provider-a`       | `provider-a-secret`       | _(nenhuma)_   |
| `provider-b`       | `provider-b-secret`       | _(nenhuma)_   |
| `internal-service` | `internal-service-secret` | `internal`    |

**Identidade → `providerId`:** usamos o claim OIDC padrão `azp`
(fallback `client_id`). `providerId == azp`. O serviço interno
simplesmente não tem `providerId` e tem a role `internal` no lugar.

**Pilha de validação** (`internal/auth`):

- `oidc.NewProvider` descobre o `.well-known/openid-configuration` do
  Keycloak no startup, baixa o JWKS e cacheia as chaves de assinatura.
  Rotação de chave é tratada pela go-oidc de forma transparente.
- O `Verifier` checa assinatura, `iss`, `exp`, `nbf` e (opcionalmente)
  `aud` contra `OIDC_AUDIENCE`.
- `ValidateBearer` extrai o subconjunto de claims que importa (`sub`,
  `azp`, `realm_access.roles`) e monta um `Principal`.

**Política HTTP** (em `httpapi/mux.go`):

| Endpoint                                         | Middleware                                               |
| ------------------------------------------------ | -------------------------------------------------------- |
| `GET /health/*`                                  | _(nenhum, público)_                                      |
| `POST /wallets`, `GET /wallets/*`                | `RequireInternal`                                        |
| `POST /wallets/*/reconciliation`                 | `RequireInternal`                                        |
| `POST /wagering/transactions`                    | `RequireProvider`                                        |
| `GET /wagering/transactions/{id}`                | `RequireAuthenticated` + checagem de provider no handler |
| `GET /providers/{pid}/wagering/transactions/{t}` | `RequireAuthenticated` + checagem de provider no handler |

**Checagens de posse** ficam no handler — a camada de service continua
ignorando quem está chamando. Isolamento entre providers é imposto:

- `POST /wagering/transactions`: `providerId` do body **tem que** ser
  igual ao `ProviderID` do token. 403 caso contrário.
- Endpoints GET: um provider só vê suas próprias linhas; devolvemos
  **404** (e não 403) em mismatch, para que um provider não consiga
  sondar os transaction IDs dos outros.

**Auth no SQS:** a política IAM do próprio broker controla quem pode
publicar na `wager-transactions.fifo`. O `providerId` dentro da
mensagem é aceito como está — o broker já provou a identidade do
remetente.

**Por que `internal` como role de realm e não como claim custom:** o
mecanismo built-in de roles do Keycloak já é serializado para
`realm_access.roles` sem precisar de nenhum protocol mapper. Menos
peças móveis.

---

## 9. Contrato HTTP

As formas exatas estão em `docs/CHALLENGE.md` §9. Mapeamento de códigos
de resposta:

| Situação                                              | Status | Code                       |
| ----------------------------------------------------- | ------ | -------------------------- |
| OK, recém-criado                                      | 201    | —                          |
| OK, replay idempotente                                | 200    | `IdempotentReplay: true`   |
| OK, pending reference                                 | 202    | status `PENDING_REFERENCE` |
| JSON malformado                                       | 400    | `INVALID_JSON`             |
| Campo obrigatório ausente                             | 400    | `MISSING_FIELD`            |
| Falha de validação de negócio                         | 400    | `INVALID_REQUEST`          |
| Kind de aposta inválido                               | 400    | `INVALID_KIND`             |
| Moeda incompatível                                    | 400    | `CURRENCY_MISMATCH`        |
| Token ausente/inválido                                | 401    | `UNAUTHENTICATED`          |
| Identidade de provider exigida                        | 403    | `FORBIDDEN`                |
| Acesso interno exigido                                | 403    | `FORBIDDEN`                |
| providerId não bate                                   | 403    | `FORBIDDEN`                |
| Wallet não encontrada                                 | 404    | `WALLET_NOT_FOUND`         |
| Transação não encontrada / escondida por provider     | 404    | `TRANSACTION_NOT_FOUND`    |
| Conflito de idempotência (mesma chave, outro payload) | 409    | `IDEMPOTENCY_CONFLICT`     |
| Wallet já existe                                      | 409    | `WALLET_ALREADY_EXISTS`    |

---

## 10. Composição Fx e shutdown

**Por que Fx:** lugar central para enxergar o grafo de dependências;
hooks de lifecycle tipados para toda goroutine de longa duração; a
ordem de shutdown gracioso é automática.

**Módulos** (`cmd/api/main.go`):

```
config.LoadConfig
logger.NewLogger
pgdb.Module           → *pgxpool.Pool  (OnStop fecha o pool)
postgres.Module       → adapters de repositório, TxRunner
service.Module        → WalletService, TransactionService
resolver.Module       → worker de pending-reference (OnStart/OnStop)
auth.Module           → Validator OIDC (discovery no startup)
sqs.Module            → *sqs.Client (resolve endpoint)
outboxpublisher.Module→ worker publisher da outbox (OnStart/OnStop)
sqsconsumer.Module    → worker consumer do SQS (OnStart/OnStop)
httpapi.Module        → *http.Server + mux (OnStart/OnStop)
```

**Ordem de startup** é função do grafo de dependências: tudo que precisa
do pool só sobe depois que `pgdb.Module` tiver sucesso; o servidor HTTP
sobe por último. Se o discovery OIDC ou o `GetQueueUrl` falhar, o
startup aborta antes de qualquer worker ser agendado.

**Ordem de shutdown** (inverso do start): HTTP server `srv.Shutdown` →
contextos dos workers cancelados → `wg.Wait()` → pool fechado.
Requisições em voo e a iteração atual do loop dos workers conseguem
terminar antes dos recursos serem liberados.

---

## 11. Observabilidade

**Logs:** JSON via `log/slog`, incluindo `wallet_id`, `transaction_id`,
`provider_id`, `event_type`, `message_id` quando aplicável. Nenhum
payload completo é logado, nenhuma credencial é logada.

**Métricas:** não implementadas nesta iteração. Pontos de ligação
planejados (ver §Limitações conhecidas) são o publisher da outbox
(profundidade da fila, latência de publicação, retries) e o consumer
(latência de receive, hits de dedup).

**Tracing:** não implementado; OpenTelemetry está marcado como opcional
no enunciado.

**Health checks:**

- `GET /health/live` — processo vivo.
- `GET /health/ready` — Postgres respondendo ao ping (e, numa iteração
  futura, SQS alcançável).

---

## 12. Estratégia de testes

**Testes unitários** vivem ao lado do código que testam. Pacotes do
domínio (`money`, `wager`, `wallet`, `ledger`, `transaction`) têm
tabelas exaustivas cobrindo parsing, overflow, invariantes, transições
de estado, regras de reversão.

**Testes de integração** ficam em `test/integration/` protegidos pela
build tag `//go:build integration`. Eles sobem um Postgres real via
`testcontainers-go`, aplicam o conjunto completo de migrations e
exercitam o caminho service → postgres. Um container por pacote via
`TestMain`; tabelas truncadas entre os testes. Veja a lista no README.
