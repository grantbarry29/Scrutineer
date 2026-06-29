/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package agentsession

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

// ApprovalPolicy is declarative-only in this slice: there is no controller gate
// yet. These specs prove the CRD schema (defaults + enum/required validation)
// is enforced by the apiserver via envtest.
var _ = Describe("ApprovalPolicy CRD", func() {
	It("accepts a valid policy and applies schema defaults", func() {
		ns := newTestNamespace()
		policy := &scrutineerv1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "prod-deploys", Namespace: ns},
			Spec: scrutineerv1alpha1.ApprovalPolicySpec{
				Actions: []string{"deploy"},
				Approvers: []scrutineerv1alpha1.ApprovalSubject{
					{Kind: scrutineerv1alpha1.ApprovalSubjectGroup, Name: "platform-oncall"},
				},
			},
		}
		Expect(k8sClient.Create(testCtx, policy)).To(Succeed())

		var got scrutineerv1alpha1.ApprovalPolicy
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(policy), &got)).To(Succeed())
		Expect(got.Spec.Requirement).To(Equal(scrutineerv1alpha1.ApprovalRequirementDefault))
		Expect(got.Spec.OnTimeout).To(Equal(scrutineerv1alpha1.ApprovalTimeoutDeny))
	})

	It("rejects an unknown onTimeout value", func() {
		ns := newTestNamespace()
		policy := &scrutineerv1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "bad-timeout", Namespace: ns},
			Spec: scrutineerv1alpha1.ApprovalPolicySpec{
				Actions:   []string{"deploy"},
				OnTimeout: scrutineerv1alpha1.ApprovalTimeoutAction("escalate"),
			},
		}
		Expect(k8sClient.Create(testCtx, policy)).NotTo(Succeed())
	})

	It("rejects a policy with no actions", func() {
		ns := newTestNamespace()
		policy := &scrutineerv1alpha1.ApprovalPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "no-actions", Namespace: ns},
			Spec:       scrutineerv1alpha1.ApprovalPolicySpec{Actions: []string{}},
		}
		Expect(k8sClient.Create(testCtx, policy)).NotTo(Succeed())
	})
})
