# connex

Voir en direct, depuis son laptop, ce qui tourne sur ses machines Linux : ports en écoute et
qui les tient, dépôts git modifiés, à pousser ou en retard, sessions d'agents (Claude Code,
Codex) qui travaillent ou attendent, applis lancées par connex. Et agir dessus : démarrer,
arrêter, relancer une appli, libérer un port, ouvrir une appli dans le navigateur.

connex est un seul binaire Go, sans démon. Le laptop joint les autres machines par ssh ; il y
pose son propre binaire au besoin, puis y lance `connex stream` le temps de l'affichage.

    connex --demo

montre l'affichage sur un simulateur de trois machines inventées. Rien n'est lu ni touché sur
la vraie machine.

## Installer

Il faut Linux (amd64 ou arm64), Go 1.27 ou plus, et `ssh` pour les machines distantes.

    go install github.com/Alexry375/connex/cmd/connex@latest

ou, depuis le dépôt :

    scripts/install.sh

Le script compile `connex` pour linux/amd64 et linux/arm64 dans `~/.local/share/connex/dist/`,
puis copie celui de la machine dans `~/.local/bin/connex`. Les machines distantes reçoivent le
leur par ssh, avec vérification de l'empreinte, au premier `connex` ou `connex ls --all` qui
les contacte. Avec `go install`, lancer aussi `scripts/install.sh` une fois pour avoir les
binaires à poser ailleurs.

## Configurer

`~/.config/connex/config.toml` :

    name = "laptop"                    # le nom de cette machine

    [[machines]]
    name = "vps"
    ssh = "vps"                        # alias de ~/.ssh/config, ou utilisateur@hôte
    public = true                      # joignable depuis Internet : alertes d'exposition

    [[machines]]
    name = "pi"
    ssh = "pi@raspberrypi"

La lecture est stricte : une clé inconnue ou un nom en double est une erreur. Sans fichier,
connex ne regarde que la machine locale.

## Commandes

    connex                      l'affichage en direct, sur toutes les machines de la configuration
    connex --demo               le même affichage, sur le simulateur (rien n'est touché)
    connex ls [--all] [--json]  relevé : ports, dépôts, agents, applis
    connex watch                les changements, en texte, avec les alertes d'exposition
    connex run <nom> [--port N] -- <commande> {port}
                                inscrire une appli (unité systemd --user connex-<nom>)
    connex start|stop|restart <nom>, connex free <port>, connex rm <nom>
    connex open <nom> [--on M]  ouvrir une appli dans le navigateur (par Tailscale)
    connex help agents          le mode d'emploi pour les agents

`connex help` donne toutes les options.

`connex train` et `--view train` servent un « mode train » : regarder une appli distante
par un miroir local quand le réseau est mauvais. Ils s'appuient sur un script de miroir
personnel, `apercu-vps`, qui n'est pas fourni ici.

## Ce que connex ne fait jamais

- Rien n'écoute : ni port, ni socket.
- Aucune commande libre : les seules actions sont celles de la liste ci-dessus. Dans
  l'affichage, chacune, sauf l'ouverture dans le navigateur, passe par une question.
- Jamais la ligne de commande ni l'environnement d'un processus, jamais le contenu d'un projet
  ni d'une conversation d'agent. D'une session Claude Code, connex ne lit que `pid` et
  `status` ; de son terminal, seulement l'heure de dernière écriture.
- Aucune écriture dans `~/.claude/`, `~/.codex/` ou `~/.agents/`.
- `git` toujours avec `--no-optional-locks`, et jamais `fetch` : « en retard » compare avec la
  branche distante connue au dernier fetch.
- Jamais root ni sudo.

## L'exposition d'un port

- `loopback` : la machine elle-même.
- `host` : la machine et ses conteneurs (pont Docker, interfaces virtuelles).
- `tailnet` : le réseau Tailscale.
- `all` : toutes les interfaces, donc joignable de l'extérieur ; ou publié sur Internet par
  Tailscale Funnel, que connex lit par `tailscale serve status --json`.

`connex watch` et `connex ls --all` écrivent une ligne `ALERTE` pour ce qui est ouvert à tous
sur une machine `public = true`, et pour tout port publié par Funnel.

## Pour les agents

    connex help agents

affiche le mode d'emploi de connex pour Claude Code et Codex : comment lire
`connex ls --all --json` et `connex watch --json`, le sens de chaque champ, ce que fait
chaque action, et leurs règles (ne rien arrêter qui ne soit à l'utilisateur, demander avant
`stop`, `free`, `restart` ou `rm`, ne jamais lire une ligne de commande de processus).

Le skill Claude Code `skill/connex/SKILL.md` le résume et y renvoie. Il n'est installé nulle
part par ce dépôt : pour l'utiliser, copier le dossier `skill/connex/` dans `~/.claude/skills/`.

## L'affichage (« l'Atlas »)

- La **planche I** montre le ciel et le catalogue de chaque machine. Le titre, en bas, dit
  qui t'attend et où.
- `/` cherche : un nombre cherche un port, des lettres un mot.
- Entrée ouvre la **planche II** de l'objet choisi. On y agit :
  - `o` ouvrir l'appli dans le navigateur ;
  - `d` démarrer, `a` arrêter, `r` relancer ;
  - `l` libérer le port.

  Chaque action sauf `o` passe par une question. Entrée y répond oui, et la question se retire
  seule si l'objet change.
- Tab ouvre le journal (planche III), `?` la légende (planche IV).
- `q` ou Ctrl-C quittent, à tout moment.

Le ciel vient de quatre planches des *Trouvelot Astronomical Drawings* d'Étienne Léopold
Trouvelot (publiées en 1881-1882), dans le domaine public, réduites et intégrées au binaire.

La branche `retable` garde un autre affichage, essayé puis écarté. Il reprend des tableaux de
Hilma af Klint (*Les Dix Plus Grands*, 1907, et les *Retables*, 1915), aussi dans le domaine
public.

## Compiler

    CGO_ENABLED=0 go build -o connex ./cmd/connex
    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o dist/connex-arm64 ./cmd/connex

`go test ./...` lance les tests.

## Licence

MIT, voir `LICENSE`.
