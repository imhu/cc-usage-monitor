package core

type ProviderKind string

const (
	ProviderClaude      ProviderKind = "claude"
	ProviderAntigravity ProviderKind = "antigravity"
)

func NormalizeProvider(p ProviderKind) ProviderKind {
	if p == ProviderAntigravity {
		return ProviderAntigravity
	}
	return ProviderClaude
}
