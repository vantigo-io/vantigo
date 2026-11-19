package brandscale

// OKLab is hex in OKLab, for the property tests.
func OKLab(hex string) (l, a, b float64) {
	r, g, bl, _ := parseHex(hex)
	c := toOKLab(r, g, bl)
	return c.l, c.a, c.b
}

// ShadeLinear is the linear sRGB of each of the ten shades of hex before any
// rounding to 8 bits, for the in-gamut assertion.
func ShadeLinear(hex string) (out [10][3]float64) {
	r, g, b, _ := parseHex(hex)
	base := toOKLab(r, g, b)
	for i := range out {
		out[i][0], out[i][1], out[i][2] = linear(shade(base, i))
	}
	return out
}
