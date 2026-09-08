package domain

import (
	"math"
	"testing"
)

func TestBudget(t *testing.T) {
	cases := []struct {
		input string
		minor int64
		valid bool
	}{
		{
			input: "19.99 usd",
			minor: 1999,
			valid: true,
		},
		{
			input: "0 USD",
			valid: true,
		},
		{
			input: " 1.2 USD ",
			minor: 120,
			valid: true,
		},
		{
			input: "92233720368547758.07 USD",
			minor: math.MaxInt64,
			valid: true,
		},
		{
			input: "92233720368547758.08 USD",
		},
		{
			input: "-1 USD",
		},
		{
			input: "1.999 USD",
		},
		{
			input: "NaN USD",
		},
		{
			input: "19.99",
		},
		{
			input: "1e3 USD",
		},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			money, err := ParseBudget(tc.input)
			if (err == nil) != tc.valid || (tc.valid && (money.Minor != tc.minor || money.Currency != "USD")) {
				t.Fatalf("got %v, %v", money, err)
			}
		})
	}
}

func TestEvaluation(t *testing.T) {
	p := Price{
		Availability: "priced",
		SaleState:    "on_sale",
		Currency:     "USD",
		Final:        new(int64(1000)),
	}
	known, qualifies := p.Evaluate(UnderBudget, new(int64(1000)), "USD")
	if !known || !qualifies {
		t.Fatal("inclusive budget should qualify")
	}

	known, qualifies = p.Evaluate(UnderBudget, new(int64(999)), "USD")
	if !known || qualifies {
		t.Fatal("price above budget should be known but unqualified")
	}

	known, _ = p.Evaluate(UnderBudget, new(int64(1000)), "EUR")
	if known {
		t.Fatal("currency mismatch must be unknown")
	}

	p.Availability = "unknown"
	known, _ = p.Evaluate(AnySale, nil, "")
	if known {
		t.Fatal("missing price must remain unknown")
	}
}
