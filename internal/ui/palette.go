package ui

import "github.com/Alexry375/connex/internal/ui/canvas"

// Palette tirée des quatre planches : nuit vert-de-gris des planches I à III,
// rayons pâles de l'aurore, lueur saumon de son horizon, or et bleu des têtes de
// météores, rouges des protubérances, papier crème et encre sépia des marges.
var (
	// Ciel et terre.
	nightTop     = canvas.RGB{R: 0.043, G: 0.071, B: 0.078}
	nightMid     = canvas.RGB{R: 0.075, G: 0.118, B: 0.122}
	nightLow     = canvas.RGB{R: 0.137, G: 0.184, B: 0.176}
	salmonGlow   = canvas.RGB{R: 0.42, G: 0.30, B: 0.27}
	landTop      = canvas.RGB{R: 0.035, G: 0.047, B: 0.045}
	silhouette   = canvas.RGB{R: 0.016, G: 0.022, B: 0.022}
	rayCore      = canvas.RGB{R: 0.86, G: 0.92, B: 0.84}
	rayGlow      = canvas.RGB{R: 0.45, G: 0.60, B: 0.54}
	rayForeign   = canvas.RGB{R: 0.40, G: 0.46, B: 0.50}
	flameRed     = canvas.RGB{R: 0.88, G: 0.20, B: 0.16}
	meteorHead   = canvas.RGB{R: 1.00, G: 0.97, B: 0.88}
	meteorBlue   = canvas.RGB{R: 0.56, G: 0.72, B: 1.00}
	meteorGold   = canvas.RGB{R: 1.00, G: 0.82, B: 0.48}
	meteorGreen  = canvas.RGB{R: 0.62, G: 0.86, B: 0.66}
	eclipseShade = canvas.RGB{R: 0.030, G: 0.034, B: 0.040}

	// Encres sur la nuit.
	ink       = canvas.Hex(0xd8e3d6)
	inkBright = canvas.Hex(0xf4f1e6)
	inkDim    = canvas.Hex(0x8ea39b)
	inkFaint  = canvas.Hex(0x5b6e69)
	inkGhost  = canvas.Hex(0x3d4b48)
	gold      = canvas.Hex(0xf0c46e)
	goldDim   = canvas.Hex(0xa98a52)
	red       = canvas.Hex(0xe8574a)
	redDim    = canvas.Hex(0x9c3a33)
	salmon    = canvas.Hex(0xe79d86)
	mint      = canvas.Hex(0xa9dcc0)
	blue      = canvas.Hex(0x8fb8ff)

	// Fonds.
	landBG      = canvas.Hex(0x090c0b)
	selectBG    = canvas.Hex(0x1a2b2a)
	selectBGDim = canvas.Hex(0x121c1c)

	// Papier.
	paper      = canvas.Hex(0xe6dfcf)
	paperInk   = canvas.Hex(0x2c2723)
	paperFaint = canvas.Hex(0x7d7365)
	paperRed   = canvas.Hex(0xa3222a)
	paperGold  = canvas.Hex(0x8a5a12)
)
