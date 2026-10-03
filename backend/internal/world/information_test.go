package world

import "testing"

func TestCurrencyDenominationsAndExactChange(t *testing.T) {
	sterling := Currency{Name: "鲁恩货币", Denominations: []Denomination{{"金镑", 960}, {"苏勒", 48}, {"便士", 4}, {"四分之一便士", 1}}}
	credits := Currency{Name: "维修积分", Denominations: []Denomination{{"积分", 100}, {"分", 1}}}
	for _, tc := range []struct {
		currency Currency
		amount   int
		want     string
	}{
		{sterling, 8352, "8 金镑 14 苏勒"}, {sterling, 8357, "8 金镑 14 苏勒 1 便士 1 四分之一便士"}, {sterling, 0, "0 四分之一便士"}, {credits, 1205, "12 积分 5 分"},
	} {
		if err := tc.currency.Validate(); err != nil {
			t.Fatal(err)
		}
		if got := tc.currency.Format(tc.amount); got != tc.want {
			t.Fatalf("got %q, want %q", got, tc.want)
		}
	}
}

func TestCurrencyRejectsAmbiguousOrLossyUnits(t *testing.T) {
	for _, denominations := range [][]Denomination{nil, {{"coin", 0}}, {{"coin", 2}}, {{"coin", 1}, {"small", 1}}, {{"coin", 10}, {"small", 3}, {"tiny", 1}}, {{"coin", 10}, {"coin", 1}}, {{"", 1}}} {
		if err := (Currency{Name: "fixture", Denominations: denominations}).Validate(); err == nil {
			t.Errorf("accepted %#v", denominations)
		}
	}
}
