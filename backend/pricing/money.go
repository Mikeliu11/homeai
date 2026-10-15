// Package pricing is the deterministic engine: it prices bill lines against
// structured, confirmed terms. It never calls a model and never reads memory.
package pricing

import (
	"errors"
	"fmt"
	"math"
	"strings"
)

// Cents is a money amount in integer cents.
type Cents int64

// ParseCents parses "$1,234.50", "22", "-$5". Anything else is an error.
func ParseCents(s string) (Cents, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty amount")
	}
	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	}
	s = strings.TrimPrefix(s, "$")
	s = strings.ReplaceAll(s, ",", "")
	if s == "" {
		return 0, errors.New("empty amount")
	}
	whole, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		whole, frac = s[:i], s[i+1:]
	}
	if whole == "" {
		whole = "0"
	}
	if len(frac) > 2 {
		return 0, fmt.Errorf("too many decimals in %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	var n int64
	for _, part := range []string{whole, frac} {
		for _, r := range part {
			if r < '0' || r > '9' {
				return 0, fmt.Errorf("not an amount: %q", s)
			}
			n = n*10 + int64(r-'0')
		}
	}
	if neg {
		n = -n
	}
	return Cents(n), nil
}

// String renders "$1,234.50" or "$22" (no decimals when whole).
func (c Cents) String() string {
	neg := c < 0
	if neg {
		c = -c
	}
	whole, frac := int64(c)/100, int64(c)%100
	ws := fmt.Sprintf("%d", whole)
	var b strings.Builder
	for i, r := range ws {
		if i > 0 && (len(ws)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := "$" + b.String()
	if frac != 0 {
		out += fmt.Sprintf(".%02d", frac)
	}
	if neg {
		out = "-" + out
	}
	return out
}

// PerHour is an hourly rate.
type PerHour Cents

// ForMinutes returns the amount for a duration, rounded to the nearest cent.
func (r PerHour) ForMinutes(min int) Cents {
	return Cents(math.Round(float64(r) * float64(min) / 60))
}

// Times multiplies a rate by a factor (e.g. Sunday ×1.5), rounded to the cent.
func (r PerHour) Times(f float64) PerHour {
	return PerHour(math.Round(float64(r) * f))
}

// TimesForMinutes is rate × factor for a duration, rounded once to the cent
// (rounding the multiplied rate first can leave a bill a cent "over").
func (r PerHour) TimesForMinutes(f float64, min int) Cents {
	return Cents(math.Round(float64(r) * f * float64(min) / 60))
}
