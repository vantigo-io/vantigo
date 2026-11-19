package brandscale

// OKLab is hex in OKLab, for the property tests.
func OKLab(hex string) (l, a, b float64) {
	r, g, bl, _ := parseHex(hex)
	c := toOKLab(r, g, bl)
	return c.l, c.a, c.b
}
