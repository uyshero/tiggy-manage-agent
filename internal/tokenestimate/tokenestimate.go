package tokenestimate

// Text estimates tokenizer usage without binding the runtime to one provider.
// ASCII text is typically close to four characters per token, while non-ASCII
// code points are conservatively treated as one token each.
func Text(value string) int {
	total := 0
	asciiRunes := 0
	flushASCII := func() {
		if asciiRunes > 0 {
			total += (asciiRunes + 3) / 4
			asciiRunes = 0
		}
	}
	for _, current := range value {
		if current <= 0x7f {
			asciiRunes++
			continue
		}
		flushASCII()
		total++
	}
	flushASCII()
	return total
}
