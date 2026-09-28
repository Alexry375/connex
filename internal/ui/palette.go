package ui

import "github.com/Alexry375/connex/internal/ui/canvas"

// La palette est prise dans les quatre tableaux (échantillonnée à la pipette
// dans inputs/univers) : le noir et l'or du Retable, l'orange, le crème et le
// bleu des spirales de Jeunesse, le vert d'Âge adulte, le bleu pâle de
// Vieillesse. Leur rose et leur violet restent aux tableaux. Le fond est le
// noir brun du Retable, plus chaud que celui du terminal : connex a sa propre
// toile.
var (
	cGround  = canvas.Hex(0x1c1817) // noir du Retable
	cGround2 = canvas.Hex(0x231e1c) // noir soulevé : ligne choisie, cartouche
	cGround3 = canvas.Hex(0x2d2724)

	cInk      = canvas.Hex(0xe8dace) // blanc cassé de Jeunesse : le texte
	cInkSoft  = canvas.Hex(0xbcae9f)
	cInkDim   = canvas.Hex(0x857970)
	cInkFaint = canvas.Hex(0x544b46)
	cInkGhost = canvas.Hex(0x3b3431)

	cGold       = canvas.Hex(0xe8b174) // feuille d'or du soleil : un agent t'attend
	cGoldDeep   = canvas.Hex(0xb5823f)
	cGoldBright = canvas.Hex(0xf7d9a4)
	cBronze     = canvas.Hex(0x6b5234) // le soleil quand personne n'attend

	cVermilion = canvas.Hex(0xe0603f) // ouvert à tous
	cRed       = canvas.Hex(0xd24a3a) // échec, refus
	cOrange    = canvas.Hex(0xd98c4a) // dépôt modifié, réponse HTTP en erreur
	cSage      = canvas.Hex(0x93b384) // appli en marche
	cSky       = canvas.Hex(0x9fb6de) // bleu des spirales
	cTeal      = canvas.Hex(0x84b1ac) // bleu-vert d'une colonne du Retable
)

// Les colonnes de la pyramide du Retable, de gauche à droite (sa colonne
// violette devient un bleu de nuit) : elles teintent les bandes de l'échelle.
var pyramid = [7]canvas.Color{
	canvas.Hex(0xb8392e), // rouge
	canvas.Hex(0xd39148), // orange
	canvas.Hex(0xc69f78), // beige
	canvas.Hex(0x52452c), // colonne des graines d'or
	canvas.Hex(0x4f7f8f), // bleu gris
	canvas.Hex(0x3a5a9a), // bleu
	canvas.Hex(0x2c3d63), // bleu de nuit
}

// astreTint est la couleur propre de chaque machine, dans l'ordre d'apparition :
// l'orange de Jeunesse, l'olive d'Âge adulte, le bleu pâle de Vieillesse.
var astreTint = []canvas.Color{
	canvas.Hex(0xe3a172),
	canvas.Hex(0xbcbd7c),
	canvas.Hex(0xa8c0d8),
}

// machineTint renvoie la couleur de la machine de rang i.
func machineTint(i int) canvas.Color {
	if i < 0 {
		return cInkSoft
	}
	return astreTint[i%len(astreTint)]
}

// stratumTint est la couleur de la marche : celle de sa colonne du Retable.
func stratumTint(s stratum) canvas.Color {
	switch s {
	case stAgents:
		return cSky
	case stApps:
		return cSage
	case stOpen:
		return cVermilion
	case stPorts:
		return canvas.Hex(0xc69f78)
	case stRepos:
		return cTeal
	}
	return cInkDim
}

func toneColor(t tone) canvas.Color {
	switch t {
	case toneGood:
		return cSage
	case toneWarn:
		return cOrange
	case toneAlert:
		return cRed
	case toneCall:
		return cGold
	case toneOpen:
		return cVermilion
	}
	return cInkSoft
}
