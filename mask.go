package audit

import (
	"net"

	secure "github.com/soulteary/secure-kit/v2"
)

// MaskDestination masks a destination (phone or email) based on channel
func MaskDestination(dest string, channel string) string {
	if dest == "" {
		return ""
	}

	switch channel {
	case "sms", "phone":
		return secure.MaskPhone(dest)
	case "email":
		return secure.MaskEmail(dest)
	default:
		// Unknown channel, mask everything
		return "****"
	}
}

// MaskEmail masks an email address
func MaskEmail(email string) string {
	return secure.MaskEmail(email)
}

// MaskPhone masks a phone number
func MaskPhone(phone string) string {
	return secure.MaskPhone(phone)
}

// MaskIP masks an IP address, keeping the first and last IPv4 octet
// (192.168.1.5 -> 192.***.5).
//
// Note this is pseudonymisation, not anonymisation: with the first and last
// octet retained an address is narrowed to 1 of 65536, and often far fewer in
// practice. Use MaskString if a stronger reduction is required.
func MaskIP(ip string) string {
	if ip == "" {
		return ""
	}

	// An IPv4-mapped IPv6 address contains dots, so the IPv4 branch below would
	// otherwise turn "::ffff:192.168.1.1" into "::ffff:192.***.1" and leak more
	// than intended. Normalise it to its IPv4 form first.
	if parsed := net.ParseIP(ip); parsed != nil {
		if v4 := parsed.To4(); v4 != nil {
			ip = v4.String()
		}
	}

	// Handle IPv4
	if len(ip) >= 7 { // Minimum valid IP: x.x.x.x
		// Find positions of dots
		firstDot := -1
		lastDot := -1
		for i, c := range ip {
			if c == '.' {
				if firstDot == -1 {
					firstDot = i
				}
				lastDot = i
			}
		}

		if firstDot != -1 && lastDot != firstDot {
			// Mask middle octets
			return ip[:firstDot+1] + "***" + ip[lastDot:]
		}
	}

	// For IPv6 or invalid format, return masked
	if len(ip) > 8 {
		return ip[:4] + "****" + ip[len(ip)-4:]
	}

	return "****"
}

// MaskString masks a string, keeping first and last n characters.
// A negative keepChars is treated as 0.
func MaskString(s string, keepChars int) string {
	if s == "" {
		return ""
	}
	// Guard the slice expressions below: a negative keepChars used to panic
	// with a slice bounds error.
	if keepChars < 0 {
		keepChars = 0
	}

	runes := []rune(s)
	length := len(runes)

	if length <= keepChars*2 {
		return "****"
	}

	return string(runes[:keepChars]) + "****" + string(runes[length-keepChars:])
}
