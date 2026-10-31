# Tailscale verification

The Tailscale runtime uses the official multi-architecture image pinned in
`internal/vpn/tailscale_runtime.go`. `TestTailscaleKernelEngine` characterizes
its kernel TUN, private daemon socket and firewall support without credentials:

```sh
go test -tags engine ./internal/vpn -run TestTailscaleKernelEngine -v
```

This does not verify enrollment, MagicDNS, peer routing, Serve or Funnel. Those
require a dedicated test tailnet and are exercised through a separate test
instance; never reuse another computer's Tailscale state or production secrets.

The agent keeps the exact test-instance address and walkthrough in its handover.
