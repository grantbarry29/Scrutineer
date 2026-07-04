package main

import (
	"fmt"
	"os"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
	"github.com/grantbarry29/scrutineer/internal/enforcement/envoy"
)

func main() {
	plain := envoy.BootstrapYAML(envoy.BootstrapConfig{Port: envoy.ProxyPort})
	rbac := envoy.BootstrapYAML(envoy.BootstrapConfig{Port: envoy.ProxyPort, Mode: scrutineerv1alpha1.PolicyModeEnforced, DeniedDomains: []string{"evil.example"}})
	os.WriteFile("/tmp/envoy-plain.yaml", []byte(plain), 0o644)
	os.WriteFile("/tmp/envoy-rbac.yaml", []byte(rbac), 0o644)
	fmt.Println("rendered")
}
