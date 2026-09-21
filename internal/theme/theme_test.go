package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad_MapsDistinctHexes(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
mode = "dark"
background = "#101010"
darker_background = "#020202"
foreground = "#F0F0F0"
muted = "#A0A0A0"
accent = "#6FB3D2"
green = "#86BA6A"
red = "#E0736B"
yellow = "#D9A441"
selection = "#1D2430"
`)
	p, rep := Load(home)
	if rep.MissingFile || rep.InvalidTOML {
		t.Fatalf("%+v", rep)
	}
	if p.Hex["surface"] != "#101010" || p.Hex["border"] != "#A0A0A0" || p.Hex["primary"] != "#F0F0F0" {
		t.Fatalf("%v", p.Hex)
	}
	if p.Hex["muted"] != "#A0A0A0" || p.Hex["secondary"] != "#A0A0A0" {
		t.Fatalf("muted/secondary %v", p.Hex)
	}
	if p.Hex["accent"] != "#6FB3D2" || p.Hex["success"] != "#86BA6A" || p.Hex["danger"] != "#E0736B" {
		t.Fatalf("%v", p.Hex)
	}
	if p.Hex["warning"] != "#D9A441" || p.Hex["selection"] != "#1D2430" {
		t.Fatalf("%v", p.Hex)
	}
	if len(rep.FallbackRoles) != 0 {
		t.Fatalf("fallback %v", rep.FallbackRoles)
	}
}

// Omarchy themes often use muted as a surface tone. Secondary text must use
// the dimmest theme token that is readable on the background.
func TestLoad_SecondaryTextIsReadable(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		{"dim muted skips to light_foreground (beirut-noir)", `
mode = "dark"
background = "#0C080C"
foreground = "#F0E5D3"
muted = "#322938"
dark_foreground = "#6D6478"
light_foreground = "#CBBFAB"
`, "#CBBFAB"},
		{"readable dark_foreground wins over light_foreground (osaka-jade)", `
mode = "dark"
background = "#111c18"
foreground = "#C1C497"
muted = "#53685B"
dark_foreground = "#81B8A8"
light_foreground = "#D6D5BC"
`, "#81B8A8"},
		{"readable muted is kept (vantablack)", `
mode = "dark"
background = "#000000"
foreground = "#ffffff"
muted = "#7a7a7a"
dark_foreground = "#505050"
light_foreground = "#ececec"
`, "#7A7A7A"},
		{"light theme (catppuccin-latte)", `
mode = "light"
background = "#eff1f5"
foreground = "#4c4f69"
muted = "#acb0be"
dark_foreground = "#9ca0b0"
light_foreground = "#5c5f77"
`, "#5C5F77"},
		{"nothing readable falls back to foreground", `
mode = "dark"
background = "#101010"
foreground = "#EEEEEE"
muted = "#202020"
`, "#EEEEEE"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			writeTheme(t, home, tc.body)
			p, _ := Load(home)
			if p.Hex["muted"] != tc.want || p.Hex["secondary"] != tc.want {
				t.Fatalf("muted=%s secondary=%s want %s", p.Hex["muted"], p.Hex["secondary"], tc.want)
			}
			if c := contrast(p.Hex["muted"], p.Hex["surface"]); c < minTextContrast {
				t.Fatalf("contrast %.2f < %.1f", c, minTextContrast)
			}
		})
	}
}

func TestFallbackSecondaryIsReadable(t *testing.T) {
	for name, fb := range map[string]map[string]string{"dark": darkFallback(), "light": lightFallback()} {
		if c := contrast(fb["muted"], fb["surface"]); c < minTextContrast {
			t.Errorf("%s fallback muted contrast %.2f", name, c)
		}
	}
}

func TestContrast(t *testing.T) {
	if c := contrast("#000000", "#FFFFFF"); c < 20.9 || c > 21.1 {
		t.Fatalf("black/white = %.2f, want 21", c)
	}
	if c := contrast("#777777", "#777777"); c != 1 {
		t.Fatalf("same color = %.2f, want 1", c)
	}
}

func TestLoad_MissingAndInvalid(t *testing.T) {
	p, rep := Load(t.TempDir())
	if !rep.MissingFile || p.Hex["surface"] == "" {
		t.Fatalf("%+v %+v", p, rep)
	}
	home := t.TempDir()
	writeTheme(t, home, "this is { not toml")
	_, rep = Load(home)
	if !rep.InvalidTOML {
		t.Fatalf("%+v", rep)
	}
}

func TestLoad_PerRoleFallback(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
mode = "dark"
background = "#111111"
foreground = "nope"
accent = "#ABCDEF"
`)
	p, rep := Load(home)
	if p.Hex["surface"] != "#111111" {
		t.Fatalf("surface %s", p.Hex["surface"])
	}
	if p.Hex["accent"] != "#ABCDEF" {
		t.Fatalf("accent %s", p.Hex["accent"])
	}
	if p.Hex["primary"] == "#111111" || p.Hex["primary"] == "" {
		t.Fatal("primary should fall back")
	}
	found := false
	for _, r := range rep.FallbackRoles {
		if r == "primary" {
			found = true
		}
	}
	if !found {
		t.Fatalf("fallback roles %v", rep.FallbackRoles)
	}
}

func writeTheme(t *testing.T, home, body string) {
	t.Helper()
	dir := filepath.Join(home, ".local", "state", "omarchy", "current", "theme")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "colors.toml"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestLoad_FaintIsDimmerButLegible(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
mode = "dark"
background = "#0C080C"
foreground = "#F0E5D3"
dark_foreground = "#6D6478"
light_foreground = "#CBBFAB"
muted = "#322938"
`)
	p, _ := Load(home)
	if p.Hex["secondary"] != "#CBBFAB" {
		t.Fatalf("secondary %s", p.Hex["secondary"])
	}
	if p.Hex["faint"] != "#6D6478" {
		t.Fatalf("faint %s", p.Hex["faint"])
	}
}

func TestLoad_FaintFallsBackToSecondary(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
background = "#000000"
foreground = "#FFFFFF"
muted = "#111111"
`)
	p, _ := Load(home)
	if p.Hex["faint"] != p.Hex["secondary"] {
		t.Fatalf("faint %s secondary %s", p.Hex["faint"], p.Hex["secondary"])
	}
}

func TestLoad_AccentGuards(t *testing.T) {
	cases := []struct {
		name, body, want string
	}{
		// Accent equal to yellow makes focus look like work in progress; the
		// theme's own blue keeps the replacement inside the theme.
		{"accent is yellow (beirut-noir)", `
background = "#0C080C"
foreground = "#F0E5D3"
accent = "#E8A63F"
yellow = "#E8A63F"
red = "#D9744E"
blue = "#92A0B9"
`, "#92A0B9"},
		{"accent is red, no usable blue (batroun)", `
background = "#101010"
foreground = "#F0F0F0"
accent = "#D45D5D"
red = "#D45D5D"
`, darkFallback()["accent"]},
		{"accent too dim to read", `
background = "#101010"
foreground = "#F0F0F0"
accent = "#202a40"
`, darkFallback()["accent"]},
		// The fallback is checked as well: here the theme's red is the
		// fallback's blue, so the violet second choice is used.
		{"fallback collides with red", `
background = "#101010"
foreground = "#F0F0F0"
accent = "#202a40"
red = "` + darkFallback()["accent"] + `"
`, darkFallback()["accent2"]},
		{"a good accent is kept", `
background = "#101010"
foreground = "#F0F0F0"
accent = "#6FB3D2"
red = "#E0736B"
yellow = "#D9A441"
green = "#86BA6A"
`, "#6FB3D2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			home := t.TempDir()
			writeTheme(t, home, c.body)
			p, _ := Load(home)
			if p.Hex["accent"] != c.want {
				t.Fatalf("accent %s, want %s", p.Hex["accent"], c.want)
			}
		})
	}
}

func TestLoad_BrandIsOrangeElseAccent(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
background = "#101010"
foreground = "#F0F0F0"
accent = "#6FB3D2"
orange = "#E28A42"
`)
	if p, _ := Load(home); p.Hex["brand"] != "#E28A42" {
		t.Fatalf("brand %s", p.Hex["brand"])
	}
	writeTheme(t, home, `
background = "#101010"
foreground = "#F0F0F0"
accent = "#6FB3D2"
`)
	if p, _ := Load(home); p.Hex["brand"] != "#6FB3D2" {
		t.Fatalf("brand %s", p.Hex["brand"])
	}
}

func TestLoad_InvisibleSelectionFallsBackToReverse(t *testing.T) {
	home := t.TempDir()
	writeTheme(t, home, `
background = "#101010"
foreground = "#F0F0F0"
selection = "#101010"
`)
	if p, _ := Load(home); !p.NoBand {
		t.Fatal("a band the colour of the background should be dropped")
	}
	// Sizes and states on the selected row are secondary text; a band that
	// sinks them is dropped even when names stay readable.
	writeTheme(t, home, `
background = "#000000"
foreground = "#FFFFFF"
dark_foreground = "#777777"
selection = "#555555"
`)
	if p, _ := Load(home); !p.NoBand {
		t.Fatal("a band that hides secondary text should be dropped")
	}
	writeTheme(t, home, `
background = "#101010"
foreground = "#F0F0F0"
selection = "#2C3144"
`)
	if p, _ := Load(home); p.NoBand {
		t.Fatal("a visible band should be kept")
	}
}
