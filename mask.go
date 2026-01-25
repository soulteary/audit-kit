package audit

import (
	secure "github.com/soulteary/secure-kit"
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

// MaskIP masks an IP address (keeps first and last octet)
func MaskIP(ip string) string {
	if ip == "" {
		return ""
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

// MaskString masks a string, keeping first and last n characters
func MaskString(s string, keepChars int) string {
	if s == "" {
		return ""
	}

	runes := []rune(s)
	length := len(runes)

	if length <= keepChars*2 {
		return "****"
	}

	return string(runes[:keepChars]) + "****" + string(runes[length-keepChars:])
}
