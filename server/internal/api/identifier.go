package api

import (
	"errors"
	"strings"
)

var errBadIdentifier = errors.New("identifier must be an email address or a phone number")

// splitIdentifier turns whatever the user typed into (email, phone).
//
// Phone numbers are normalized to E.164 because the same Nigerian number gets
// typed at least four ways — 08031234567, 8031234567, +2348031234567,
// 2348031234567 — and a user who signs up one way and logs in another must land
// on the same account.
func splitIdentifier(raw string) (email, phone string, err error) {
	v := strings.TrimSpace(raw)
	if v == "" {
		return "", "", errBadIdentifier
	}
	if strings.Contains(v, "@") {
		v = strings.ToLower(v)
		if len(v) < 5 || strings.HasPrefix(v, "@") || strings.HasSuffix(v, "@") || !strings.Contains(v, ".") {
			return "", "", errBadIdentifier
		}
		return v, "", nil
	}
	p, err := normalizePhone(v)
	if err != nil {
		return "", "", err
	}
	return "", p, nil
}

// normalizePhone converts Nigerian local formats to E.164 and passes through
// other countries' numbers that already carry a '+'.
func normalizePhone(raw string) (string, error) {
	var digits strings.Builder
	plus := strings.HasPrefix(strings.TrimSpace(raw), "+")
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()

	switch {
	case plus:
		if len(d) < 8 || len(d) > 15 {
			return "", errBadIdentifier
		}
		return "+" + d, nil
	case strings.HasPrefix(d, "234") && len(d) == 13:
		return "+" + d, nil
	case strings.HasPrefix(d, "0") && len(d) == 11:
		// 08031234567 -> +2348031234567
		return "+234" + d[1:], nil
	case len(d) == 10:
		// 8031234567 -> +2348031234567
		return "+234" + d, nil
	default:
		return "", errBadIdentifier
	}
}
