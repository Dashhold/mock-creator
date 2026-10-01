package render

import (
	"embed"
	"io/fs"
)

// assets holds the Typst template, the Dashhold logo and the Noto fonts
// (OFL-1.1; licences alongside). They are embedded so a render needs nothing
// from the host except the typst binary.
//
//go:embed assets/paper.typ assets/logo.png assets/fonts/*.ttf assets/fonts/*.license
var assets embed.FS

// Logo returns the Dashhold-EdTech logo PNG.
func Logo() []byte {
	b, _ := assets.ReadFile("assets/logo.png")
	return b
}

func fontFiles() ([]string, error) {
	return fs.Glob(assets, "assets/fonts/*.ttf")
}
