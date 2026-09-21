package theme

import (
	"image/color"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Palette holds Tori chrome colors. Values are concrete RGBA (not Lipgloss).
type Palette struct {
	Surface   color.Color
	Border    color.Color
	Primary   color.Color
	Secondary color.Color
	Muted     color.Color
	Accent    color.Color
	Success   color.Color
	Danger    color.Color
	Warning   color.Color
	Selection color.Color
	// Faint is for disabled hints: dimmer than Secondary but still legible.
	Faint color.Color
	// Brand is the wordmark's colour and nothing else.
	Brand color.Color
	// NoBand means the selection colour cannot be told apart from the
	// background, or text on it is hard to read, so the selected row is
	// drawn in reverse video instead.
	NoBand bool
	Hex    map[string]string
}

// Report notes which roles used fallback.
type Report struct {
	FallbackRoles []string
	MissingFile   bool
	InvalidTOML   bool
}

type fileColors struct {
	Mode            string `toml:"mode"`
	Background      string `toml:"background"`
	Foreground      string `toml:"foreground"`
	DarkForeground  string `toml:"dark_foreground"`
	LightForeground string `toml:"light_foreground"`
	Muted           string `toml:"muted"`
	Accent          string `toml:"accent"`
	Green           string `toml:"green"`
	Red             string `toml:"red"`
	Yellow          string `toml:"yellow"`
	Orange          string `toml:"orange"`
	Blue            string `toml:"blue"`
	Selection       string `toml:"selection"`
}

func darkFallback() map[string]string {
	return map[string]string{
		"surface":   "#1B1D27",
		"border":    "#3A3D4A",
		"primary":   "#EDE6DA",
		"secondary": "#8A8494",
		"muted":     "#8A8494",
		"accent":    "#6FA3D8",
		"success":   "#7FB069",
		"danger":    "#D45D5D",
		"warning":   "#D4A017",
		"selection": "#2C3144",
		"faint":     "#6B6578",
		"brand":     "#E0A45E",
		"accent2":   "#B39DDB",
	}
}

func lightFallback() map[string]string {
	return map[string]string{
		"surface":   "#F4F1EA",
		"border":    "#C9C2B6",
		"primary":   "#2A2A32",
		"secondary": "#6E6878",
		"muted":     "#6E6878",
		"accent":    "#3D6FA8",
		"success":   "#3F7A3A",
		"danger":    "#B04040",
		"warning":   "#A07A10",
		"selection": "#D9E2F2",
		"faint":     "#8E8898",
		"brand":     "#96590B",
		"accent2":   "#6A4C9C",
	}
}

// Base is the built-in palette for a dark or light terminal, used when
// there is no Omarchy theme to read.
func Base(dark bool) Palette {
	if dark {
		return paletteFromHex(darkFallback())
	}
	return paletteFromHex(lightFallback())
}

// Load is total: a broken theme never fails the TUI.
func Load(home string) (Palette, Report) {
	fb := darkFallback()
	path := filepath.Join(home, ".local", "state", "omarchy", "current", "theme", "colors.toml")
	data, err := os.ReadFile(path)
	if err != nil {
		return paletteFromHex(fb), Report{MissingFile: true, FallbackRoles: allRoles()}
	}
	var fc fileColors
	if _, err := toml.Decode(string(data), &fc); err != nil {
		if strings.EqualFold(strings.TrimSpace(fc.Mode), "light") {
			fb = lightFallback()
		}
		return paletteFromHex(fb), Report{InvalidTOML: true, FallbackRoles: allRoles()}
	}
	if strings.EqualFold(strings.TrimSpace(fc.Mode), "light") {
		fb = lightFallback()
	}
	hex := map[string]string{}
	var fell []string
	put := func(role, raw string) {
		if h, ok := parseHex(raw); ok {
			hex[role] = h
			return
		}
		hex[role] = fb[role]
		fell = append(fell, role)
	}
	put("surface", fc.Background)
	// Omarchy themes use muted as a surface/border tone (see
	// hyprland_inactive_border), and darker_background is almost the same
	// as background, so a border drawn in it cannot be seen.
	put("border", fc.Muted)
	put("primary", fc.Foreground)
	// muted is too dim to read as text in most themes. Use the dimmest
	// theme token that is still readable on the background, else foreground.
	text := readableText(hex["surface"], hex["primary"], fc.Muted, fc.DarkForeground, fc.LightForeground)
	hex["muted"] = text
	hex["secondary"] = text
	hex["faint"] = faintText(hex["surface"], text, fc.DarkForeground, fc.Muted)
	put("accent", fc.Accent)
	put("success", fc.Green)
	put("danger", fc.Red)
	put("warning", fc.Yellow)
	put("selection", fc.Selection)
	if !guardAccent(hex, fb, fc.Blue) {
		fell = append(fell, "accent")
	}
	// Omarchy has no brand tone; orange is the nearest thing a theme defines.
	hex["brand"] = hex["accent"]
	if h, ok := parseHex(fc.Orange); ok {
		hex["brand"] = h
	}
	return paletteFromHex(hex), Report{FallbackRoles: fell}
}

// guardAccent keeps focus readable and unmistakable. The accent marks the
// selected row and the active tab, so it must clear body contrast, and it
// must not share a hue with danger, warning or success: a theme whose
// accent is its red makes every selection look like a failure, and one
// whose accent is its yellow makes focus look like work in progress. The
// theme's own blue is tried first so the replacement still belongs to the
// theme. It reports whether the theme's accent was kept.
func guardAccent(hex, fb map[string]string, blue string) bool {
	usable := func(h string) bool {
		if contrast(h, hex["surface"]) < minTextContrast {
			return false
		}
		for _, role := range []string{"danger", "warning", "success"} {
			if near(h, hex[role]) {
				return false
			}
		}
		return true
	}
	if usable(hex["accent"]) {
		return true
	}
	// The fallback is checked too: a theme can define a red that happens to
	// sit on the fallback's blue. Violet is the second choice because no
	// status colour uses it. With nothing usable the fallback is still the
	// best guess, being designed for this mode.
	for _, raw := range []string{blue, fb["accent"], fb["accent2"]} {
		if h, ok := parseHex(raw); ok && usable(h) {
			hex["accent"] = h
			return false
		}
	}
	hex["accent"] = fb["accent"]
	return false
}

// nearDistance is how close two colours can sit in RGB space before they
// read as the same hue at a glance.
const nearDistance = 48

func near(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	ar, ag, ab, _ := mustRGBA(a).RGBA()
	br, bg, bb, _ := mustRGBA(b).RGBA()
	d := func(x, y uint32) float64 { return float64(x>>8) - float64(y>>8) }
	return math.Sqrt(d(ar, br)*d(ar, br)+d(ag, bg)*d(ag, bg)+d(ab, bb)*d(ab, bb)) < nearDistance
}

// noBand reports whether a selection band would be invisible against the
// background or would make the text on it hard to read. Selected rows carry
// sizes and states in secondary as well as names in primary. The name must
// stay fully readable; the metadata only legible, since the band lifts the
// background and would otherwise fail most themes' secondary text,
// including the fallbacks'.
func noBand(hex map[string]string) bool {
	return contrast(hex["selection"], hex["surface"]) < 1.05 ||
		contrast(hex["primary"], hex["selection"]) < minTextContrast ||
		contrast(hex["secondary"], hex["selection"]) < minFaintContrast
}

// minTextContrast is the WCAG AA contrast ratio for normal text.
const minTextContrast = 4.5

// readableText returns the first candidate that parses and reaches
// minTextContrast against surface, or primary when none does.
func readableText(surface, primary string, candidates ...string) string {
	for _, raw := range candidates {
		if h, ok := parseHex(raw); ok && contrast(h, surface) >= minTextContrast {
			return h
		}
	}
	return primary
}

// contrast is the WCAG 2 contrast ratio between two #RRGGBB colors.
func contrast(a, b string) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func luminance(hex string) float64 {
	r, g, b, _ := mustRGBA(hex).RGBA()
	lin := func(v uint32) float64 {
		c := float64(v>>8) / 255
		if c <= 0.03928 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
}

// minFaintContrast keeps disabled text legible (WCAG exempts disabled
// controls from 4.5:1, but below 3:1 they vanish).
const minFaintContrast = 3.0

// faintText returns the first candidate between minFaintContrast and the
// secondary color's contrast, so it reads as dimmer than secondary text.
func faintText(surface, secondary string, candidates ...string) string {
	if surface == "" || secondary == "" {
		return secondary
	}
	top := contrast(secondary, surface)
	for _, raw := range candidates {
		if h, ok := parseHex(raw); ok {
			c := contrast(h, surface)
			if c >= minFaintContrast && c < top {
				return h
			}
		}
	}
	return secondary
}

func allRoles() []string {
	return []string{"surface", "border", "primary", "secondary", "muted", "accent", "success", "danger", "warning", "selection"}
}

func parseHex(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) != 7 || s[0] != '#' {
		return "", false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return "", false
		}
	}
	return strings.ToUpper(s[:1] + s[1:]), true
}

func paletteFromHex(hex map[string]string) Palette {
	p := Palette{Hex: hex}
	p.Surface = mustRGBA(hex["surface"])
	p.Border = mustRGBA(hex["border"])
	p.Primary = mustRGBA(hex["primary"])
	p.Secondary = mustRGBA(hex["secondary"])
	p.Muted = mustRGBA(hex["muted"])
	p.Accent = mustRGBA(hex["accent"])
	p.Success = mustRGBA(hex["success"])
	p.Danger = mustRGBA(hex["danger"])
	p.Warning = mustRGBA(hex["warning"])
	p.Selection = mustRGBA(hex["selection"])
	p.Faint = mustRGBA(hex["faint"])
	if hex["faint"] == "" {
		p.Faint = p.Secondary
	}
	p.Brand = mustRGBA(hex["brand"])
	if hex["brand"] == "" {
		p.Brand = p.Accent
	}
	p.NoBand = noBand(hex)
	return p
}

func mustRGBA(hex string) color.Color {
	if hex == "" {
		return color.RGBA{R: 0, G: 0, B: 0, A: 255}
	}
	var r, g, b uint8
	_, _ = parseByte(hex[1:3], &r)
	_, _ = parseByte(hex[3:5], &g)
	_, _ = parseByte(hex[5:7], &b)
	return color.RGBA{R: r, G: g, B: b, A: 255}
}

func parseByte(s string, dst *uint8) (int, error) {
	var n uint8
	for i := 0; i < len(s); i++ {
		n <<= 4
		c := s[i]
		switch {
		case c >= '0' && c <= '9':
			n |= c - '0'
		case c >= 'a' && c <= 'f':
			n |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			n |= c - 'A' + 10
		}
	}
	*dst = n
	return 1, nil
}
