package models

import "testing"

func TestSubscription_CanUseVideoImport(t *testing.T) {
	cases := []struct {
		name string
		tier SubscriptionTier
		used int
		want bool
	}{
		{"free under cap", TierFree, 1, true},
		{"free at cap", TierFree, 2, false},
		{"free over cap", TierFree, 5, false},
		{"premium under cap", TierPremium, 19, true},
		{"premium at cap", TierPremium, 20, false},
		{"premium over cap", TierPremium, 25, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &Subscription{Tier: tc.tier, VideoImportsUsed: tc.used}
			if got := s.CanUseVideoImport(); got != tc.want {
				t.Errorf("CanUseVideoImport(tier=%s, used=%d) = %v, want %v", tc.tier, tc.used, got, tc.want)
			}
		})
	}
}
