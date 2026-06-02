# VHS tape for the kubetidy README demo GIF.
# Regenerate with:  make demo-gif   (needs a running kind demo + Prometheus; see the target)
#
# Records a real `kubectl tidy scan` at Tier 1 (Prometheus) against the kind demo namespace
# (the over-provisioned load generators in hack/kind/demo-workloads.yaml), showing the card
# layout: per-workload usage distribution (avg/p95/p99/peak) and the request → proposed change.
#
# Docs: https://github.com/charmbracelet/vhs

Output docs/assets/demo.gif

Require kubectl

Set Shell bash
Set FontSize 15
Set Width 1240
Set Height 1000
Set Padding 22
Set Margin 0
Set Theme "Dracula"
Set TypingSpeed 32ms

# Setup (hidden): clean prompt, our binary on PATH, isolated kubeconfig for the kind cluster.
Hide
Type "export PS1='$ ' PATH=$PWD/bin:$PATH KUBECONFIG=/tmp/kubetidy-demo.kubeconfig" Enter
Type "clear" Enter
Show

Sleep 700ms
Type "kubectl tidy scan -n kubetidy-demo --prometheus-url http://localhost:9090 --window 1h" Sleep 700ms Enter
Sleep 7s
