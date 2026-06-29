/*
Copyright 2026 The Scrutineer Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package v1alpha1

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	scrutineerv1alpha1 "github.com/grantbarry29/scrutineer/api/v1alpha1"
)

var arSeq int

func newAR(decision scrutineerv1alpha1.ApprovalDecision, decidedBy string) *scrutineerv1alpha1.ApprovalRequest {
	arSeq++
	return &scrutineerv1alpha1.ApprovalRequest{
		ObjectMeta: metav1.ObjectMeta{Name: fmt.Sprintf("wh-ar-%d", arSeq), Namespace: "default"},
		Spec: scrutineerv1alpha1.ApprovalRequestSpec{
			SessionRef: scrutineerv1alpha1.ApprovalSessionRef{Name: "sess"},
			Action:     "deploy",
			Decision:   decision,
			DecidedBy:  decidedBy,
		},
	}
}

var _ = Describe("ApprovalRequest identity webhook (envtest)", func() {
	It("stamps the authenticated approver on a grant, ignoring the client-sent value", func() {
		alice := clientAs("alice", "platform-oncall")
		ar := newAR(scrutineerv1alpha1.ApprovalDecisionGranted, "mallory") // spoof attempt
		Expect(alice.Create(testCtx, ar)).To(Succeed())

		var got scrutineerv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(ar), &got)).To(Succeed())
		Expect(got.Spec.DecidedBy).To(Equal("alice"), "decidedBy must be the authenticated identity, not the spoofed value")
	})

	It("leaves decidedBy empty for a pending (controller-style) create", func() {
		ar := newAR(scrutineerv1alpha1.ApprovalDecisionPending, "")
		Expect(k8sClient.Create(testCtx, ar)).To(Succeed())

		var got scrutineerv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(ar), &got)).To(Succeed())
		Expect(got.Spec.DecidedBy).To(BeEmpty(), "a request with no decision must not be attributed to anyone")
	})

	It("corrects a spoofed decidedBy when the decision is set on update", func() {
		// Controller-style pending create first.
		ar := newAR(scrutineerv1alpha1.ApprovalDecisionPending, "")
		Expect(k8sClient.Create(testCtx, ar)).To(Succeed())

		// Bob grants it but tries to attribute the decision to someone else.
		bob := clientAs("bob")
		var live scrutineerv1alpha1.ApprovalRequest
		Expect(bob.Get(testCtx, client.ObjectKeyFromObject(ar), &live)).To(Succeed())
		live.Spec.Decision = scrutineerv1alpha1.ApprovalDecisionGranted
		live.Spec.DecidedBy = "alice" // attribute to someone else
		Expect(bob.Update(testCtx, &live)).To(Succeed())

		var got scrutineerv1alpha1.ApprovalRequest
		Expect(k8sClient.Get(testCtx, client.ObjectKeyFromObject(ar), &got)).To(Succeed())
		Expect(got.Spec.DecidedBy).To(Equal("bob"), "decidedBy must reflect who actually made the decision")
	})
})
