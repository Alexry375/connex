# connex — branche `retable`

Un second affichage de connex, essayé puis écarté : « le retable ». Son univers vient de
tableaux de Hilma af Klint (*Les Dix Plus Grands*, 1907, et les *Retables*, 1915), dans le
domaine public, réduits et intégrés au binaire. Un soleil au centre, et les objets rangés en
marches selon leur urgence.

Cette branche date d'avant la lecture des machines distantes et des applis. Elle n'est gardée
que pour comparer les deux affichages, sur le simulateur :

    CGO_ENABLED=0 go build -o connex-retable ./cmd/connex
    ./connex-retable --demo --seed 7

L'affichage retenu, « l'Atlas », et tout le reste de connex sont sur la branche `main`.

## Licence

MIT, voir `LICENSE`.
