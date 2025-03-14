package codegen

// VLQ encode a number into variable-length base64 encoding
func VLQEncode(value int) string {
	chunks := []byte{}
	first := true
	negative := value < 0

	if negative {
		value = -value
	}

	if value == 0 {
		return "A"
	}

	// first chunk:
	// bit 0: sign bit
	// bit 1-4: 4-bit value chunk
	// bit 5: continuation bit

	// subsequent chunks:
	// bit 0-4: 5-bit value chunk
	// bit 5: continuation bit
	for value > 0 {
		var chunk int

		if first {
			chunk = (value & 0xF) << 1 // Get the 4-bit value chunk
			value >>= 4
			if negative {
				chunk |= 0x01
			}
		} else {
			chunk = value & 0x1F // Get the 6-bit value chunk
			value >>= 5
		}

		if value > 0 {
			// If there are more bits to encode, set the continuation bit
			chunk |= 0x20
		}

		// Append the chunk to the result
		chunks = append(chunks, byte(chunk))
		first = false
	}

	var base64Chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	encoded := ""
	for _, chunk := range chunks {
		// Map the chunk to a base64 character
		encoded += string(base64Chars[chunk])
	}

	return encoded
}
