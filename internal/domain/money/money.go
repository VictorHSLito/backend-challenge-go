package money

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
)

var (
	ErrInvalidMoney     = errors.New("invalid or uninitialized money")
	ErrCurrencyMismatch = errors.New("currency mismatch between money values")
	ErrMoneyOverflow    = errors.New("money operation overflow")
)

var (
	amountRegex   = regexp.MustCompile(`^(0|[1-9][0-9]*)\.[0-9]{2}$`)
	currencyRegex = regexp.MustCompile(`^[A-Z]{3}$`)
)

type Money struct {
	amount   int64
	currency string
}

func validateCurrency(currency string) error {
	if !currencyRegex.MatchString(currency) {
		return fmt.Errorf("%w: invalid currency %q", ErrInvalidMoney, currency)
	}
	return nil
}

func New(units int64, currency string) (Money, error) {
	if err := validateCurrency(currency); err != nil {
		return Money{}, err
	}
	return Money{amount: units, currency: currency}, nil
}

func Zero(currency string) (Money, error) {
	return New(0, currency)
}

func Parse(amountStr, currency string) (Money, error) {
	if err := validateCurrency(currency); err != nil {
		return Money{}, err
	}
	if !amountRegex.MatchString(amountStr) {
		return Money{}, fmt.Errorf("%w: invalid amount format %q", ErrInvalidMoney, amountStr)
	}

	dot := len(amountStr) - 3
	intPart := amountStr[:dot]
	fracPart := amountStr[dot+1:]

	units, err := strconv.ParseInt(intPart, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: integer part out of range", ErrMoneyOverflow)
	}
	cents, err := strconv.ParseInt(fracPart, 10, 64)
	if err != nil {
		return Money{}, fmt.Errorf("%w: fractional part invalid", ErrInvalidMoney)
	}

	if units > math.MaxInt64/100 {
		return Money{}, fmt.Errorf("%w: amount*100 overflow", ErrMoneyOverflow)
	}
	scaled := units * 100
	if scaled > math.MaxInt64-cents {
		return Money{}, fmt.Errorf("%w: amount*100+cents overflow", ErrMoneyOverflow)
	}

	return Money{amount: scaled + cents, currency: currency}, nil
}

func (m Money) validate() error {
	if m.currency == "" {
		return ErrInvalidMoney
	}
	return nil
}

func (m Money) Amount() int64    { return m.amount }
func (m Money) Currency() string { return m.currency }

func (m Money) IsValid() bool    { return m.currency != "" }
func (m Money) IsZero() bool     { return m.currency != "" && m.amount == 0 }
func (m Money) IsNegative() bool { return m.currency != "" && m.amount < 0 }
func (m Money) IsPositive() bool { return m.currency != "" && m.amount > 0 }

func (m Money) Equal(other Money) bool {
	return m.currency != "" && other.currency != "" &&
		m.amount == other.amount && m.currency == other.currency
}

func (m Money) checkPair(other Money) error {
	if err := m.validate(); err != nil {
		return err
	}
	if err := other.validate(); err != nil {
		return err
	}
	if m.currency != other.currency {
		return ErrCurrencyMismatch
	}
	return nil
}

func (m Money) Add(other Money) (Money, error) {
	if err := m.checkPair(other); err != nil {
		return Money{}, err
	}
	if (other.amount > 0 && m.amount > math.MaxInt64-other.amount) ||
		(other.amount < 0 && m.amount < math.MinInt64-other.amount) {
		return Money{}, ErrMoneyOverflow
	}
	return Money{amount: m.amount + other.amount, currency: m.currency}, nil
}

func (m Money) Subtract(other Money) (Money, error) {
	if err := m.checkPair(other); err != nil {
		return Money{}, err
	}
	if (other.amount < 0 && m.amount > math.MaxInt64+other.amount) ||
		(other.amount > 0 && m.amount < math.MinInt64+other.amount) {
		return Money{}, ErrMoneyOverflow
	}
	return Money{amount: m.amount - other.amount, currency: m.currency}, nil
}

func (m Money) Negate() (Money, error) {
	if err := m.validate(); err != nil {
		return Money{}, err
	}
	if m.amount == math.MinInt64 {
		return Money{}, ErrMoneyOverflow
	}
	return Money{amount: -m.amount, currency: m.currency}, nil
}

func (m Money) Compare(other Money) (int, error) {
	if err := m.checkPair(other); err != nil {
		return 0, err
	}
	switch {
	case m.amount < other.amount:
		return -1, nil
	case m.amount > other.amount:
		return 1, nil
	default:
		return 0, nil
	}
}

func (m Money) MarshalJSON() ([]byte, error) {
	if err := m.validate(); err != nil {
		return nil, err
	}
	return []byte(fmt.Sprintf(`{"amount":%q,"currency":%q}`, formatAmount(m.amount), m.currency)), nil
}

func (m *Money) UnmarshalJSON(data []byte) error {
	var aux struct {
		Amount   string `json:"amount"`
		Currency string `json:"currency"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	parsed, err := Parse(aux.Amount, aux.Currency)
	if err != nil {
		return err
	}
	*m = parsed
	return nil
}

func formatAmount(cents int64) string {
	abs := uint64(cents)
	if cents < 0 {
		abs = -abs
	}
	s := fmt.Sprintf("%d.%02d", abs/100, abs%100)
	if cents < 0 {
		return "-" + s
	}
	return s
}
