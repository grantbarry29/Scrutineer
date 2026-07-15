//go:build e2e

/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package e2e

import (
	"os"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
)

// generatedReporterRolePath is the marker-generated reporter ClusterRole
// (config/rbac/reporter/role.yaml, produced by controller-gen from the
// +kubebuilder:rbac markers in internal/reporter/server.go, per #3).
const generatedReporterRolePath = "../../config/rbac/reporter/role.yaml"

// reporterRoleRules parses the rules of the generated reporter-role ClusterRole. It is
// the single source of the reporter's RBAC for every e2e grant: both the in-cluster
// reporter (deployInClusterReporter) and the standalone-overlay reporter
// (deployStandaloneReporterRBAC) bind exactly these rules to their reporter SA, rather
// than a hand-maintained copy that could silently drift from what the binary needs
// (#151). Removing a verb from the markers and regenerating flows straight through here:
// the reporter SA loses the permission and its status writes 403, turning the live
// specs red — the drift no longer hides behind a green suite.
func reporterRoleRules() []rbacv1.PolicyRule {
	GinkgoHelper()
	raw, err := os.ReadFile(generatedReporterRolePath)
	Expect(err).NotTo(HaveOccurred(), "read %s", generatedReporterRolePath)

	// role.yaml is a single ClusterRole document (with a leading `---`); pick it out by
	// kind so a leading separator or future sibling docs don't trip the parse.
	var role *rbacv1.ClusterRole
	for _, doc := range strings.Split(string(raw), "\n---") {
		if strings.TrimSpace(doc) == "" {
			continue
		}
		var tm metav1.TypeMeta
		Expect(yaml.Unmarshal([]byte(doc), &tm)).To(Succeed())
		if tm.Kind == "ClusterRole" {
			role = &rbacv1.ClusterRole{}
			Expect(yaml.Unmarshal([]byte(doc), role)).To(Succeed())
		}
	}
	Expect(role).NotTo(BeNil(), "%s must contain a ClusterRole", generatedReporterRolePath)
	Expect(role.Name).To(Equal("reporter-role"), "generated reporter role must be named reporter-role")
	Expect(role.Rules).NotTo(BeEmpty(), "generated reporter-role must carry rules")
	return role.Rules
}
