package money

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
)

func TestParseValid(t *testing.T) {
	cases := []struct {
		input    string
		expected int64
	}{
		{"0.00", 0},
		{"25.00", 2500},
		{"25.50", 2550},
		{"1000.00", 100000},
		{"92233720368547758.07", math.MaxInt64},
	}
	for _, tc := range cases {
		m, err := Parse(tc.input, "BRL")
		if err != nil {
			t.Fatalf("Parse(%q): unexpected error %v", tc.input, err)
		}
		if m.Amount() != tc.expected {
			t.Errorf("Parse(%q).Amount() = %d; want %d", tc.input, m.Amount(), tc.expected)
		}
		if m.Currency() != "BRL" {
			t.Errorf("Parse(%q).Currency() = %q; want BRL", tc.input, m.Currency())
		}
	}
}

func TestParseInvalidFormat(t *testing.T) {
	for _, in := range []string{
		"", "-1.00", "1e2", "NaN", "Infinity",
		"1.000", "01.00", "25,00", "+25.00",
		"25", "25.5", "25.", ".25", " 25.00", "25.00 ",
	} {
		_, err := Parse(in, "BRL")
		if !errors.Is(err, ErrInvalidMoney) {
			t.Errorf("Parse(%q): expected ErrInvalidMoney, got %v", in, err)
		}
	}
}

func TestParseOverflow(t *testing.T) {
	for _, in := range []string{
		"92233720368547758.08",
		"92233720368547759.00",
		"99999999999999999999.00",
	} {
		_, err := Parse(in, "BRL")
		if !errors.Is(err, ErrMoneyOverflow) {
			t.Errorf("Parse(%q): expected ErrMoneyOverflow, got %v", in, err)
		}
	}
}

func TestInvalidCurrency(t *testing.T) {
	for _, cur := range []string{"", "br", "BRLL", "BR1", "123", "brl", " BRL", "BRL "} {
		if _, err := Parse("25.00", cur); !errors.Is(err, ErrInvalidMoney) {
			t.Errorf("Parse currency=%q: expected ErrInvalidMoney, got %v", cur, err)
		}
		if _, err := New(100, cur); !errors.Is(err, ErrInvalidMoney) {
			t.Errorf("New currency=%q: expected ErrInvalidMoney, got %v", cur, err)
		}
		if _, err := Zero(cur); !errors.Is(err, ErrInvalidMoney) {
			t.Errorf("Zero currency=%q: expected ErrInvalidMoney, got %v", cur, err)
		}
	}
}

func TestZero(t *testing.T) {
	m, err := Zero("BRL")
	if err != nil {
		t.Fatalf("Zero: %v", err)
	}
	if !m.IsZero() {
		t.Error("Zero().IsZero() = false; want true")
	}
	if m.IsNegative() {
		t.Error("Zero().IsNegative() = true; want false")
	}
	if m.Currency() != "BRL" {
		t.Errorf("Zero currency = %q; want BRL", m.Currency())
	}
}

func TestAddAndSubtract(t *testing.T) {
	m25, _ := Parse("25.00", "BRL")
	m10, _ := Parse("10.00", "BRL")
	usd, _ := Parse("10.00", "USD")

	sum, err := m25.Add(m10)
	if err != nil || sum.Amount() != 3500 {
		t.Errorf("Add: got (%d, %v); want (3500, nil)", sum.Amount(), err)
	}

	diff, err := m10.Subtract(m25)
	if err != nil || diff.Amount() != -1500 {
		t.Errorf("Subtract negative: got (%d, %v); want (-1500, nil)", diff.Amount(), err)
	}
	if !diff.IsNegative() {
		t.Error("Subtract result should report IsNegative")
	}
	if diff.Currency() != "BRL" {
		t.Errorf("Subtract preserved currency = %q; want BRL", diff.Currency())
	}

	if _, err := m25.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Add mismatched currency: got %v; want ErrCurrencyMismatch", err)
	}
	if _, err := m25.Subtract(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Subtract mismatched currency: got %v; want ErrCurrencyMismatch", err)
	}

	var empty Money
	if _, err := empty.Add(m25); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Add uninitialized receiver: got %v; want ErrInvalidMoney", err)
	}
	if _, err := m25.Add(empty); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Add uninitialized operand: got %v; want ErrInvalidMoney", err)
	}
}

func TestAddOverflow(t *testing.T) {
	hi, err := New(math.MaxInt64, "BRL")
	if err != nil {
		t.Fatalf("New max: %v", err)
	}
	one, _ := Parse("0.01", "BRL")
	if _, err := hi.Add(one); !errors.Is(err, ErrMoneyOverflow) {
		t.Errorf("Add overflow: got %v; want ErrMoneyOverflow", err)
	}

	lo, _ := New(math.MinInt64, "BRL")
	negOne, _ := New(-1, "BRL")
	if _, err := lo.Add(negOne); !errors.Is(err, ErrMoneyOverflow) {
		t.Errorf("Add negative overflow: got %v; want ErrMoneyOverflow", err)
	}
}

func TestSubtractOverflow(t *testing.T) {
	lo, _ := New(math.MinInt64, "BRL")
	one, _ := Parse("0.01", "BRL")
	if _, err := lo.Subtract(one); !errors.Is(err, ErrMoneyOverflow) {
		t.Errorf("Subtract overflow: got %v; want ErrMoneyOverflow", err)
	}

	hi, _ := New(math.MaxInt64, "BRL")
	negOne, _ := New(-1, "BRL")
	if _, err := hi.Subtract(negOne); !errors.Is(err, ErrMoneyOverflow) {
		t.Errorf("Subtract negative overflow: got %v; want ErrMoneyOverflow", err)
	}
}

func TestNegate(t *testing.T) {
	m, _ := Parse("25.00", "BRL")
	neg, err := m.Negate()
	if err != nil || neg.Amount() != -2500 {
		t.Errorf("Negate: got (%d, %v); want (-2500, nil)", neg.Amount(), err)
	}
	back, err := neg.Negate()
	if err != nil || !back.Equal(m) {
		t.Errorf("Negate round-trip failed: back=%+v err=%v", back, err)
	}

	min, _ := New(math.MinInt64, "BRL")
	if _, err := min.Negate(); !errors.Is(err, ErrMoneyOverflow) {
		t.Errorf("Negate MinInt64: got %v; want ErrMoneyOverflow", err)
	}

	var empty Money
	if _, err := empty.Negate(); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Negate uninitialized: got %v; want ErrInvalidMoney", err)
	}
}

func TestCompare(t *testing.T) {
	a, _ := Parse("10.00", "BRL")
	b, _ := Parse("20.00", "BRL")
	if c, _ := a.Compare(b); c != -1 {
		t.Errorf("Compare(10,20) = %d; want -1", c)
	}
	if c, _ := b.Compare(a); c != 1 {
		t.Errorf("Compare(20,10) = %d; want 1", c)
	}
	if c, _ := a.Compare(a); c != 0 {
		t.Errorf("Compare(10,10) = %d; want 0", c)
	}

	usd, _ := Parse("10.00", "USD")
	if _, err := a.Compare(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Errorf("Compare mismatched: got %v; want ErrCurrencyMismatch", err)
	}

	var empty Money
	if _, err := empty.Compare(a); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Compare uninitialized receiver: got %v; want ErrInvalidMoney", err)
	}
	if _, err := a.Compare(empty); !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Compare uninitialized operand: got %v; want ErrInvalidMoney", err)
	}
}

func TestIsValidAndIsPositive(t *testing.T) {
	var empty Money
	if empty.IsValid() {
		t.Error("uninitialized IsValid should be false")
	}
	if empty.IsPositive() {
		t.Error("uninitialized IsPositive should be false")
	}

	zero, _ := Zero("BRL")
	if !zero.IsValid() {
		t.Error("Zero IsValid should be true")
	}
	if zero.IsPositive() {
		t.Error("Zero IsPositive should be false")
	}

	pos, _ := Parse("10.00", "BRL")
	if !pos.IsValid() {
		t.Error("positive IsValid should be true")
	}
	if !pos.IsPositive() {
		t.Error("positive IsPositive should be true")
	}

	neg, _ := New(-100, "BRL")
	if !neg.IsValid() {
		t.Error("negative IsValid should be true")
	}
	if neg.IsPositive() {
		t.Error("negative IsPositive should be false")
	}
}

func TestEqualAndPredicatesOnUninitialized(t *testing.T) {
	var m Money
	if m.IsZero() {
		t.Error("uninitialized IsZero should be false")
	}
	if m.IsNegative() {
		t.Error("uninitialized IsNegative should be false")
	}
	if m.Equal(m) {
		t.Error("uninitialized Equal(self) should be false")
	}
	ok, _ := New(0, "BRL")
	if m.Equal(ok) || ok.Equal(m) {
		t.Error("uninitialized Equal(initialized) should be false in both directions")
	}
}

func TestMarshalJSONExact(t *testing.T) {
	cases := []struct {
		cents    int64
		currency string
		want     string
	}{
		{2500, "BRL", `{"amount":"25.00","currency":"BRL"}`},
		{0, "USD", `{"amount":"0.00","currency":"USD"}`},
		{-2550, "BRL", `{"amount":"-25.50","currency":"BRL"}`},
		{1, "BRL", `{"amount":"0.01","currency":"BRL"}`},
		{-1, "BRL", `{"amount":"-0.01","currency":"BRL"}`},
		{math.MinInt64, "BRL", `{"amount":"-92233720368547758.08","currency":"BRL"}`},
		{math.MaxInt64, "BRL", `{"amount":"92233720368547758.07","currency":"BRL"}`},
	}
	for _, tc := range cases {
		m, err := New(tc.cents, tc.currency)
		if err != nil {
			t.Fatalf("New(%d, %q): %v", tc.cents, tc.currency, err)
		}
		data, err := json.Marshal(m)
		if err != nil {
			t.Fatalf("Marshal %d %s: %v", tc.cents, tc.currency, err)
		}
		if string(data) != tc.want {
			t.Errorf("Marshal(%d %s) = %s; want %s", tc.cents, tc.currency, data, tc.want)
		}
	}
}

func TestMarshalJSONUninitialized(t *testing.T) {
	var m Money
	if _, err := json.Marshal(m); err == nil {
		t.Error("Marshal uninitialized: expected error, got nil")
	}
}

func TestJSONRoundTripPositive(t *testing.T) {
	original, _ := Parse("1234.56", "BRL")
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Money
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !original.Equal(decoded) {
		t.Errorf("round-trip: got %+v; want %+v", decoded, original)
	}
}

func TestUnmarshalJSONRejectsNegative(t *testing.T) {
	var m Money
	err := json.Unmarshal([]byte(`{"amount":"-25.00","currency":"BRL"}`), &m)
	if !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Unmarshal negative: got %v; want ErrInvalidMoney", err)
	}
}

func TestUnmarshalJSONRejectsBadCurrency(t *testing.T) {
	var m Money
	err := json.Unmarshal([]byte(`{"amount":"25.00","currency":"brl"}`), &m)
	if !errors.Is(err, ErrInvalidMoney) {
		t.Errorf("Unmarshal bad currency: got %v; want ErrInvalidMoney", err)
	}
}
