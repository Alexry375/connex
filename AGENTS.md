# connex — conventions du code

- Go 1.27, `CGO_ENABLED=0` toujours : un binaire sans dépendance, pour amd64 et arm64.
- Le programme principal est dans `cmd/connex/` ; le reste dans `internal/`.
- Identifiants en anglais ; commentaires et messages pour l'utilisateur en français.
- `gofmt` et `go vet` propres ; `go test ./...` vert avant chaque commit.
- Dépendances : bibliothèque standard d'abord. Toute dépendance externe doit être sous licence
  MIT, Apache-2.0, BSD ou MPL-2.0, sans cgo, et justifiée dans le message de commit. Du code
  repris d'un autre projet garde sa notice de licence en tête de fichier.
- Le modèle de données (`schema: 1`) est un contrat : ne jamais renommer ni retirer un champ
  sans passer à `schema: 2`.
- Sécurité (non négociable) : rien n'écoute (ni port ni socket), aucune commande libre, jamais
  la ligne de commande d'un processus en sortie, jamais le contenu d'un projet ni d'une
  conversation d'agent, aucune écriture dans `~/.claude/`, `~/.codex/`, `~/.agents/`, tout
  appel à `git` avec `--no-optional-locks`, jamais root ni sudo.
- Messages de commit en français, à l'impératif.
