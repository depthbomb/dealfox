package domain

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Money struct {
	Minor    int64
	Currency string
}

type App struct {
	ID                int64
	Name              string
	Type              string
	LastModified      int64
	PriceChangeNumber int64
}

type Price struct {
	AppID        int64
	Name         string
	Type         string
	CapsuleURL   string
	Country      string
	Availability string
	SaleState    string
	Currency     string
	Regular      *int64
	Final        *int64
	Discount     *int
	ObservedAt   time.Time
	SourceStatus string
	SourceHash   string
}

type Payload struct {
	RuleID    string
	AppID     int64
	Name      string
	Country   string
	Currency  string
	Regular   int64
	Final     int64
	Discount  int
	Recurring bool
}

type PublicError struct {
	Code    string
	Message string
}

var budgetPattern = regexp.MustCompile(`^(\d+)(?:\.(\d{1,2}))?\s+([A-Za-z]{3})$`)

const (
	AnySale     = "any_sale"
	UnderBudget = "sale_under_budget"
)

func (e *PublicError) Error() string {
	return e.Message
}

func Invalid(message string) error {
	return &PublicError{
		Code:    "VALIDATION",
		Message: message,
	}
}

func ParseBudget(input string) (Money, error) {
	match := budgetPattern.FindStringSubmatch(strings.TrimSpace(input))
	if match == nil {
		return Money{}, Invalid("Budget must have the form `19.99 USD`.")
	}

	major, err := strconv.ParseInt(match[1], 10, 64)
	fraction, _ := strconv.ParseInt(match[2]+strings.Repeat("0", 2-len(match[2])), 10, 64)
	if err != nil || major > (math.MaxInt64-fraction)/100 {
		return Money{}, Invalid("Budget is too large.")
	}

	return Money{
		Minor:    major*100 + fraction,
		Currency: strings.ToUpper(match[3]),
	}, nil
}

func (m Money) String() string {
	return fmt.Sprintf("%s %d.%02d", m.Currency, m.Minor/100, m.Minor%100)
}

func (p Price) Evaluate(condition string, budget *int64, currency string) (known, qualifies bool) {
	if p.Availability != "priced" || p.SaleState == "unknown" {
		return false, false
	}

	if condition == AnySale {
		return true, p.SaleState == "on_sale"
	}

	if condition != UnderBudget || budget == nil || p.Final == nil || p.Currency != currency {
		return false, false
	}

	return true, p.SaleState == "on_sale" && *p.Final <= *budget
}
