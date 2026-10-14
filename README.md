# Jungle Gaming — Serviço de Apostas

Serviço em Go que processa operações de aposta de jogadores com garantias
financeiras rígidas sob concorrência e entrega _at-least-once_.

Enunciado completo do desafio: [`docs/CHALLENGE.md`](docs/CHALLENGE.md).
Decisões de projeto e trade-offs: [`ARCHITECTURE.md`](ARCHITECTURE.md).

---

## Começo rápido

```sh
# Um único comando sobe Postgres, Keycloak (com a realm wager já importada),
# LocalStack (com as filas FIFO provisionadas), roda as migrations e a API.
cp .env.example .env
docker compose up --build
```

A API escuta em `http://localhost:8080`. Health check:

```sh
curl http://localhost:8080/health/live
```

UI de admin do Keycloak: <http://localhost:8180> (usuário `admin` / senha `admin`).

---

## Pré-requisitos

| Ferramenta        | Para quê                                                                                         |
| ----------------- | ------------------------------------------------------------------------------------------------ |
| Docker ≥ 24       | Stack do Compose (Postgres, Keycloak, LocalStack)                                                |
| Go ≥ 1.26         | Exigido pelo `go.mod`; `GOTOOLCHAIN=auto` baixa a versão certa se o seu Go local for mais antigo |
| `curl`, `jq`      | Para os exemplos de comando abaixo                                                               |
| `make` (opcional) | Alvos de conveniência apenas, não obrigatório                                                    |

Para rodar os testes de integração é preciso adicionalmente que o daemon do
Docker esteja acessível (os testes sobem um Postgres descartável via
testcontainers).

---

## Variáveis de ambiente

Veja [`.env.example`](.env.example) para um conjunto completo e funcional.
Obrigatórias no boot (o processo falha rápido caso contrário):

| Variável           | Para que serve                                                       |
| ------------------ | -------------------------------------------------------------------- |
| `DATABASE_URL`     | DSN do Postgres                                                      |
| `OIDC_ISSUER_URL`  | URL da realm do Keycloak (ex.: `http://localhost:8180/realms/wager`) |
| `SQS_INPUT_QUEUE`  | Nome da fila FIFO de entrada de transações                           |
| `SQS_EVENTS_QUEUE` | Nome da fila FIFO de saída de eventos de carteira                    |

Opcionais:

| Variável                                      | Padrão      | Para que serve                                                                                                                                                      |
| --------------------------------------------- | ----------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `HTTP_PORT`                                   | `8080`      | Porta do servidor HTTP                                                                                                                                              |
| `OIDC_AUDIENCE`                               | _(nenhum)_  | Se definido, exige o valor no claim `aud` dos tokens                                                                                                                |
| `OIDC_INTERNAL_ROLE`                          | `internal`  | Role de realm que concede acesso interno                                                                                                                            |
| `OIDC_DISCOVERY_URL`                          | _(nenhum)_  | Opcional: URL alternativa para buscar o documento de discovery OIDC quando a API não consegue atingir `OIDC_ISSUER_URL` diretamente (ex.: dentro do Docker Compose) |
| `AWS_REGION`                                  | `us-east-1` | Região AWS do cliente SQS                                                                                                                                           |
| `SQS_ENDPOINT`                                | _(nenhum)_  | Override para LocalStack (`http://localhost:4566`)                                                                                                                  |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | _(nenhum)_  | Credenciais SQS; LocalStack aceita qualquer coisa                                                                                                                   |

---

## Rodando fora do Docker (dev local)

```sh
# 1. Suba as dependências (Postgres, Keycloak, LocalStack + migrations).
docker compose up -d postgres migrate keycloak localstack

# 2. Exporte as variáveis (ou dê source no .env).
export $(grep -v '^#' .env | xargs)

# 3. Rode a API localmente.
go run ./cmd/api
```

---

## Migrations

O Compose as executa automaticamente. Para aplicar/reverter manualmente:

```sh
docker compose run --rm migrate up        # aplica
docker compose run --rm migrate down 1    # reverte a última
```

---

## Exemplos de chamada

Obtenha tokens do Keycloak:

```sh
TOKEN_A=$(curl -s -X POST \
  http://localhost:8180/realms/wager/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=provider-a \
  -d client_secret=provider-a-secret | jq -r .access_token)

TOKEN_INT=$(curl -s -X POST \
  http://localhost:8180/realms/wager/protocol/openid-connect/token \
  -d grant_type=client_credentials \
  -d client_id=internal-service \
  -d client_secret=internal-service-secret | jq -r .access_token)
```

Abra uma carteira (apenas interno):

```sh
curl -s -X POST http://localhost:8080/wallets \
  -H "Authorization: Bearer $TOKEN_INT" \
  -H "Content-Type: application/json" \
  -d '{
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "initialBalance": { "amount": "1000.00", "currency": "BRL" }
  }' | jq .
```

Envie uma aposta como `provider-a` (substitua `<walletId>`):

```sh
curl -s -X POST http://localhost:8080/wagering/transactions \
  -H "Authorization: Bearer $TOKEN_A" \
  -H "Idempotency-Key: provider-a:tx-1" \
  -H "Content-Type: application/json" \
  -d '{
    "providerId": "provider-a",
    "externalTransactionId": "tx-1",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "<walletId>",
    "roundId": "round-1",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": { "amount": "25.00", "currency": "BRL" }
  }' | jq .
```

Envie via SQS (mesmas regras de negócio, mesma idempotência):

```sh
aws --endpoint-url=http://localhost:4566 sqs send-message \
  --queue-url http://localhost:4566/000000000000/wager-transactions.fifo \
  --message-group-id "<walletId>" \
  --message-deduplication-id "msg-001" \
  --message-body "$(cat <<EOF
{
  "messageId": "msg-001",
  "type": "WagerTransactionRequested",
  "occurredAt": "2026-10-14T12:00:00.000Z",
  "data": {
    "providerId": "provider-a",
    "externalTransactionId": "tx-sqs-1",
    "idempotencyKey": "provider-a:tx-sqs-1",
    "playerId": "0192f28f-5dc0-7d58-bdb2-814ad6a0f4a1",
    "walletId": "<walletId>",
    "roundId": "round-1",
    "gameId": "fortune-chimp",
    "kind": "BET",
    "money": { "amount": "25.00", "currency": "BRL" }
  }
}
EOF
)"
```

Observe os eventos publicados na fila de saída:

```sh
aws --endpoint-url=http://localhost:4566 sqs receive-message \
  --queue-url http://localhost:4566/000000000000/wallet-events.fifo \
  --max-number-of-messages 10 | jq .
```

---

## Testes

Testes unitários (rápidos, sem infra):

```sh
go test ./...
go test -race ./...
go vet ./...
```

Testes de integração (Postgres real via testcontainers — requer Docker):

```sh
go test -tags=integration -race -timeout=5m ./test/integration/...
```

A suíte de integração cobre:

- Abertura de carteira (positiva, zerada, rejeição por duplicidade)
- Caminho feliz de `BET` + outbox + ledger
- Rejeição por saldo insuficiente + código de falha
- Replay idempotente (mesma chave + mesmo payload)
- Conflito de idempotência (mesma chave + payload diferente)
- Teste obrigatório de concorrência: 100.00 BRL + duas apostas paralelas de 80.00
- 50 execuções paralelas da mesma aposta → um único débito
- Deduplicação via inbox do SQS através de `ProcessFromMessage`
- Detecção de messageId _poison_

---

## Layout do projeto

```
cmd/api/                 Raiz de composição — fiação do Fx + hooks de lifecycle
internal/
  auth/                  Validador JWT/OIDC + middleware HTTP
  config/                Config dirigida por env
  domain/                Camada de negócio pura — sem imports de infra
    event/               Envelope de evento + payloads tipados
    ledger/              Lançamento imutável do ledger
    money/               Value object Money em int64 (centavos)
    transaction/         Agregado WagerTransaction + state machine
    wager/               Enums Kind, Status, Direction + regras
    wallet/              Agregado Wallet
  httpapi/               Handlers HTTP, mux, fiação de auth
  integration/sqs/       Wrapper fino de aws-sdk-go-v2
  logger/                Logger JSON (slog)
  pgdb/                  Bootstrap do pgxpool + lifecycle Fx
  repository/postgres/   Adapters Postgres implementando as portas do service
  resolver/              Worker em background que resolve PENDING_REFERENCE
  service/               Casos de uso + portas de repositório (fronteira hexagonal)
  testinfra/             Helpers de testcontainers (só testes de integração)
  workers/
    outboxpublisher/     Publica outbox → SQS (FOR UPDATE SKIP LOCKED)
    sqsconsumer/         Consome wager-transactions.fifo + dedup via inbox
deploy/
  keycloak/              Realm JSON (3 clients: provider-a/-b, internal-service)
  localstack/            Script de init do SQS (filas FIFO + DLQs)
migrations/              SQL up/down versionado (migrate/migrate)
test/integration/        Testes de integração (build tag `integration`)
docs/                    CHALLENGE.md + material de referência
```

---

## O que foi feito

- Camada de domínio, fronteira hexagonal preservada
- Endpoints HTTP com auth OIDC e autorização por rota
- Persistência em Postgres com lock no nível de linha + unique constraints
- Outbox transacional + deduplicação via inbox
- Worker publisher de outbox + worker consumer de SQS
- Resolver de referências pendentes com backoff
- Stack do Compose + migrations + realm + provisionamento das filas
- Dockerfile (multi-stage, distroless, non-root)
- 11 testes de integração cobrindo concorrência e idempotência
