---
name: connex
description: Lire et expliquer ce qui tourne sur les machines de l'utilisateur (laptop, VPS, Raspberry Pi) avec connex. À charger pour toute question sur ces machines, leurs ports en écoute et qui les tient, ce qui est ouvert à tous ou publié par Funnel, les sessions d'agents (Claude Code, Codex) qui travaillent ou attendent, ce qui tourne sans raison, les dépôts git modifiés, à pousser ou en retard, et les applis lancées par connex.
---

# connex

connex montre, depuis le laptop, les ports, dépôts, sessions d'agents et applis des machines
de l'utilisateur. Le mode d'emploi complet est dans le binaire : lance d'abord

    connex help agents

et suis-le. Ce qui suit en résume les règles.

## Lire

- `connex ls --all --json` : une ligne JSON par machine (`snapshot`, ou `machine_status` si elle
  est injoignable). `connex watch --json` : le direct.
- Exposition d'un port : `loopback` (la machine), `host` (la machine et ses conteneurs),
  `tailnet` (le réseau Tailscale), `all` (ouvert à tous ; avec `funnel: true`, publié sur
  Internet par Tailscale Funnel).
- Nom du programme : `unit` sans `.service`, sinon `exe` si `process` n'est qu'un nom de fil
  (`MainThread`), sinon `process`.
- Agents : `working`, `waiting` (il a fini ou pose une question : il attend l'utilisateur), `unknown`.
- Dépôts : `dirty`, `upstream`, `ahead` (à pousser), `behind` (en retard, d'après le dernier
  fetch : connex ne contacte jamais le dépôt distant).

## Règles

- Ne jamais arrêter un processus qui n'est pas à l'utilisateur (`mine: false`).
- Toujours demander à l'utilisateur avant `stop`, `free`, `restart` ou `rm`, en disant ce qui sera
  arrêté, sur quelle machine (`--on`), et pourquoi tu le crois inutile. `free` arrête le
  processus qui tient le port.
- Ne jamais lire la ligne de commande ni l'environnement d'un processus, ni le contenu d'une
  conversation d'agent, pour « en savoir plus ».
- Ne lance ni `tailscale funnel`, ni `tailscale serve`, ni `git fetch` pour vérifier : demande.
