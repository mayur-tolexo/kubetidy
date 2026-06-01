# VHS tape for the kubetidy README demo GIF.
# Regenerate with:  make demo-gif   (needs a running kind demo + Prometheus; see the target)
#
# Records a real `kubectl tidy scan` + `kubectl tidy diff` at Tier 1 (Prometheus) against the
# kind demo namespace (the deliberately over-provisioned workloads in
# hack/kind/demo-workloads.yaml). Tier 1 gives stable, high-confidence numbers and no caveat
# banner, so the recording is reproducible.
#
# Prereqs the target sets up: kind demo running, Prometheus deployed, and a port-forward of
# prometheus-server to localhost:9090.
#
# Docs: https://github.com/charmbracelet/vhs

Output docs/assets/demo.gif

Require kubectl

Set Shell bash
Set FontSize 16
Set Width 1280
Set Height 820
Set Padding 24
Set Margin 0
Set Theme "Dracula"
Set TypingSpeed 35ms

# Setup (hidden): clean prompt, our binary on PATH, isolated kubeconfig for the kind cluster.
Hide
Type "export PS1='$ ' PATH=$PWD/bin:$PATH KUBECONFIG=/tmp/kubetidy-demo.kubeconfig" Enter
Type "clear" Enter
Show

Sleep 700ms
Type "kubectl tidy scan -n kubetidy-demo --prometheus-url http://localhost:9090 --window 1h" Sleep 700ms Enter
Sleep 4500ms

Type "kubectl tidy diff -n kubetidy-demo --prometheus-url http://localhost:9090 --window 1h" Sleep 700ms Enter
Sleep 5500ms
