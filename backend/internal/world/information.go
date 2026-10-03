package world

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

type Calendar struct {
	Kind string `json:"kind"`
	Era  string `json:"era,omitempty"`
}

type Denomination struct {
	Name  string `json:"name"`
	Units int    `json:"units"`
}

type Currency struct {
	Name          string         `json:"name"`
	Denominations []Denomination `json:"denominations"`
}

func (c Currency) Validate() error {
	if strings.TrimSpace(c.Name) == "" || utf8.RuneCountInString(c.Name) > 80 || len(c.Denominations) == 0 || len(c.Denominations) > 8 {
		return fmt.Errorf("invalid currency")
	}
	previous := math.MaxInt
	names := map[string]bool{}
	for i, unit := range c.Denominations {
		if strings.TrimSpace(unit.Name) == "" || utf8.RuneCountInString(unit.Name) > 40 || names[unit.Name] || unit.Units < 1 || unit.Units >= previous || (i > 0 && previous%unit.Units != 0) {
			return fmt.Errorf("invalid currency denominations")
		}
		names[unit.Name], previous = true, unit.Units
	}
	if previous != 1 {
		return fmt.Errorf("currency must describe its smallest unit")
	}
	return nil
}

func (c Currency) Format(amount int) string {
	parts := []string{}
	for _, unit := range c.Denominations {
		count := amount / unit.Units
		amount %= unit.Units
		if count != 0 {
			parts = append(parts, fmt.Sprintf("%d %s", count, unit.Name))
		}
	}
	if len(parts) == 0 {
		return "0 " + c.Denominations[len(c.Denominations)-1].Name
	}
	return strings.Join(parts, " ")
}

type KnownLocation struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Kind        string   `json:"kind"`
	Parent      string   `json:"parent,omitempty"`
	Connections []string `json:"connections"`
}
