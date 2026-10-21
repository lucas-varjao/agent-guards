# agent-guards

Duas ferramentas para os erros de shell que agentes (Claude Code, Codex) cometem neste devbox.

- **`waitfor`**: espera uma condição sem cair no loop que nunca termina. Cada execução da sonda
  (probe) é classificada, e erro ou saída vazia contam como "sonda quebrada", não como
  "ainda não". Três erros seguidos abortam com exit 3 em segundos, em vez de esperar o timeout
  inteiro. Veja `waitfor --help`.
- **`agent-guard`**: hook `PreToolUse` que bloqueia comandos só de bash (`mapfile`, `read -a`,
  `${!var}`...), loops de espera escritos à mão, `sleep` às cegas e streams sem limite
  (`tail -f`, `--watch`, `kubectl rollout status` sem `--timeout`). Se não conseguir ler o
  payload, deixa o comando passar.

## Instalação

```zsh
GOBIN=$HOME/.local/bin go install ./cmd/...
```

Onde cada parte está ligada:

| Peça | Arquivo |
| --- | --- |
| Hook do Claude Code | `~/.claude/settings.json` → `hooks.PreToolUse[Bash]` (ao lado do `rtk hook claude`) |
| Hook do Codex | `~/.codex/hooks.json` (o Codex pede para confiar no hook na primeira vez) |
| Opções do zsh dos agentes | `~/.zshenv`, bloco `ZSH_EXECUTION_STRING` (`no_nomatch sh_word_split no_equals no_bad_pattern`) |
| Regras para os agentes | `~/.agents/AGENTS.md`, seção *Execution policy* |

Para testar uma regra sem rodar nada: `agent-guard check -- 'kubectl get pods -w'`.
