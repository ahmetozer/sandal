package cmd

import (
	"slices"
)

func isIn(a *[]string, s string) bool {
	return slices.Contains(*a, s)
}
