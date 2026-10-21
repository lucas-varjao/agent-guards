package guard

import "testing"

func TestCheckBlocks(t *testing.T) {
	cases := []string{
		`mapfile -t files < <(ls)`,
		`ls | readarray -t x`,
		`read -ra parts <<< "$line"`,
		`shopt -s globstar`,
		`echo ${!name}`,
		`for k in "${!assoc[@]}"; do echo $k; done`,
		`echo "${BASH_SOURCE[0]}"`,
		`false | true; echo ${PIPESTATUS[0]}`,
		`echo ${name,,}`,
		`echo ${arr[0]}`,
		`declare -n ref=foo`,
		`while ! curl -s localhost:8080; do sleep 5; done`,
		"until kubectl get pod x -o jsonpath='{.status.phase}' | grep -q Running\ndo\n  sleep 10\ndone",
		`for i in {1..60}; do glab ci status && break; sleep 30; done`,
		`watch -n 5 kubectl get pods`,
		`sleep 120 && kubectl get pods`,
		`sleep 2m`,
		`kubectl --context default logs -f deploy/x`,
		`kubectl get pods -w`,
		`kubectl --context default rollout status deploy/x`,
		`journalctl -u foo -f`,
		`tail -n 50 -f /var/log/x.log`,
		`podman logs --follow web`,
		`gh run watch 123`,
		`bash -c 'while true; do sleep 1; done'`,
	}
	for _, c := range cases {
		if len(Check(c)) == 0 {
			t.Errorf("expected block: %s", c)
		}
	}
}

func TestCheckAllows(t *testing.T) {
	cases := []string{
		`curl -fsS 'https://api.example.com/x?a=b'`,
		`kubectl --context default get pods -o jsonpath='{.items[*].metadata.name}'`,
		`rg 'mapfile|readarray' scripts/`,
		`grep -n 'BASH_SOURCE' install.sh`,
		`bash -c 'mapfile -t x < f; echo ${x[0]}'`,
		`bash ./scripts/setup.sh`,
		`timeout 60s kubectl --context default logs -f deploy/x`,
		`kubectl --context default rollout status deploy/x --timeout=2m`,
		`kubectl --context default logs --tail=200 deploy/x`,
		`tail -n 100 /var/log/x.log`,
		`sleep 5`,
		`while read -r line; do echo "$line"; done < file`,
		`read -rA parts <<< "$line"`,
		`echo ${(P)name} ${arr[1]}`,
		"cat > loop.sh <<'EOF'\nwhile true; do sleep 60; done\nmapfile -t x\nEOF\nchmod +x loop.sh",
		`cat <<<"$x" | wc -l`,
		`waitfor --timeout 5m --until Running -- kubectl --context default get pod x -o jsonpath='{.status.phase}'`,
		`for f in *.go; do gofmt -l "$f"; done`,
		"(sleep 3; touch /tmp/flag) & waitfor --timeout 30s --until ok -- cat /tmp/flag",
		`echo "wait for it" && sleep 2`,
		`git commit -m 'docs: explain wait loops' # agent-guard: allow`,
	}
	for _, c := range cases {
		if fs := Check(c); len(fs) > 0 {
			t.Errorf("unexpected block: %s\n%s", c, Message(fs))
		}
	}
}
