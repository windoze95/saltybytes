package models

import "testing"

// --- IsValidAuthType ---

func TestIsValidAuthType_Standard(t *testing.T) {
	ua := &UserAuth{AuthType: Standard}
	if !ua.IsValidAuthType() {
		t.Error("IsValidAuthType(Standard) should be true")
	}
}

func TestIsValidAuthType_Invalid(t *testing.T) {
	ua := &UserAuth{AuthType: "invalid"}
	if ua.IsValidAuthType() {
		t.Error("IsValidAuthType('invalid') should be false")
	}
}

func TestIsValidAuthType_Empty(t *testing.T) {
	ua := &UserAuth{AuthType: ""}
	if ua.IsValidAuthType() {
		t.Error("IsValidAuthType('') should be false")
	}
}

// --- Subscription tier checks ---

func TestCanUseAllergenAnalysis_Free_UnderLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AllergenAnalysesUsed: 2}
	if !s.CanUseAllergenAnalysis() {
		t.Error("CanUseAllergenAnalysis: free tier with 4 uses should be true")
	}
}

func TestCanUseAllergenAnalysis_Free_AtLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AllergenAnalysesUsed: 3}
	if s.CanUseAllergenAnalysis() {
		t.Error("CanUseAllergenAnalysis: free tier with 5 uses should be false")
	}
}

func TestCanUseAllergenAnalysis_Premium(t *testing.T) {
	// Premium is capped now (12/mo): under passes, at/over gates.
	under := &Subscription{Tier: TierPremium, AllergenAnalysesUsed: 11}
	if !under.CanUseAllergenAnalysis() {
		t.Error("premium under cap should pass")
	}
	over := &Subscription{Tier: TierPremium, AllergenAnalysesUsed: 12}
	if over.CanUseAllergenAnalysis() {
		t.Error("premium at cap must gate — premium is no longer unlimited")
	}
}

func TestCanUseWebSearch_Free_UnderLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, WebSearchesUsed: 9}
	if !s.CanUseWebSearch() {
		t.Error("CanUseWebSearch: free tier with 19 uses should be true")
	}
}

func TestCanUseWebSearch_Free_AtLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, WebSearchesUsed: 10}
	if s.CanUseWebSearch() {
		t.Error("CanUseWebSearch: free tier with 20 uses should be false")
	}
}

func TestCanUseWebSearch_Premium(t *testing.T) {
	// Premium search cap is 50/mo.
	under := &Subscription{Tier: TierPremium, WebSearchesUsed: 49}
	if !under.CanUseWebSearch() {
		t.Error("premium under cap should pass")
	}
	over := &Subscription{Tier: TierPremium, WebSearchesUsed: 50}
	if over.CanUseWebSearch() {
		t.Error("premium at cap must gate")
	}
}

func TestCanUseAIGeneration_Free_UnderLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AIGenerationsUsed: 9}
	if !s.CanUseAIGeneration() {
		t.Error("CanUseAIGeneration: free tier with 49 uses should be true")
	}
}

func TestCanUseAIGeneration_Free_AtLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AIGenerationsUsed: 10}
	if s.CanUseAIGeneration() {
		t.Error("CanUseAIGeneration: free tier with 50 uses should be false")
	}
}

func TestCanUseAIGeneration_Premium(t *testing.T) {
	// Premium generation cap is 30/mo.
	under := &Subscription{Tier: TierPremium, AIGenerationsUsed: 29}
	if !under.CanUseAIGeneration() {
		t.Error("premium under cap should pass")
	}
	over := &Subscription{Tier: TierPremium, AIGenerationsUsed: 30}
	if over.CanUseAIGeneration() {
		t.Error("premium at cap must gate")
	}
}

func TestTierPlus_Caps(t *testing.T) {
	s := &Subscription{Tier: TierPlus, AIGenerationsUsed: 14, WebSearchesUsed: 19, AllergenAnalysesUsed: 4, VideoImportsUsed: 1, AIImportsUsed: 24}
	if !s.CanUseAIGeneration() || !s.CanUseWebSearch() || !s.CanUseAllergenAnalysis() || !s.CanUseVideoImport() || !s.CanUseAIImport() {
		t.Error("plus under caps should pass everywhere")
	}
	maxed := &Subscription{Tier: TierPlus, AIGenerationsUsed: 15, WebSearchesUsed: 20, AllergenAnalysesUsed: 5, VideoImportsUsed: 2, AIImportsUsed: 25}
	if maxed.CanUseAIGeneration() || maxed.CanUseWebSearch() || maxed.CanUseAllergenAnalysis() || maxed.CanUseVideoImport() || maxed.CanUseAIImport() {
		t.Error("plus at caps must gate everywhere")
	}
}

func TestTierUnlimited_BypassesEverything(t *testing.T) {
	// The hidden operator tier: absurd counters, everything still allowed.
	s := &Subscription{Tier: TierUnlimited, AIGenerationsUsed: 1 << 20, WebSearchesUsed: 1 << 20, AllergenAnalysesUsed: 1 << 20, VideoImportsUsed: 1 << 20, AIImportsUsed: 1 << 20}
	if !s.CanUseAIGeneration() || !s.CanUseWebSearch() || !s.CanUseAllergenAnalysis() || !s.CanUseVideoImport() || !s.CanUseAIImport() {
		t.Error("unlimited tier must never gate")
	}
}

func TestLimitsForTier_UnknownFailsClosedToFree(t *testing.T) {
	if LimitsForTier("enterprise") != LimitsForTier(TierFree) {
		t.Error("unknown tiers must get free-tier limits")
	}
}

// --- IsValidSubscriptionTier ---

func TestIsValidSubscriptionTier_Free(t *testing.T) {
	s := &Subscription{Tier: TierFree}
	if !s.IsValidSubscriptionTier() {
		t.Error("IsValidSubscriptionTier(TierFree) should be true")
	}
}

func TestIsValidSubscriptionTier_Premium(t *testing.T) {
	s := &Subscription{Tier: TierPremium}
	if !s.IsValidSubscriptionTier() {
		t.Error("IsValidSubscriptionTier(TierPremium) should be true")
	}
}

func TestIsValidSubscriptionTier_PlusAndUnlimited(t *testing.T) {
	for _, tier := range []SubscriptionTier{TierPlus, TierUnlimited} {
		s := &Subscription{Tier: tier}
		if !s.IsValidSubscriptionTier() {
			t.Errorf("IsValidSubscriptionTier(%q) should be true", tier)
		}
	}
}

func TestIsValidSubscriptionTier_Invalid(t *testing.T) {
	s := &Subscription{Tier: "enterprise"}
	if s.IsValidSubscriptionTier() {
		t.Error("IsValidSubscriptionTier('enterprise') should be false")
	}
}

// --- UnitSystemText ---

func TestUnitSystemText_USCustomary(t *testing.T) {
	p := &Personalization{UnitSystem: "us_customary"}
	got := p.UnitSystemText()
	if got != "US Customary" {
		t.Errorf("UnitSystemText(us_customary) = %q, want 'US Customary'", got)
	}
}

func TestUnitSystemText_Metric(t *testing.T) {
	p := &Personalization{UnitSystem: "metric"}
	got := p.UnitSystemText()
	if got != "Metric" {
		t.Errorf("UnitSystemText(metric) = %q, want 'Metric'", got)
	}
}

func TestUnitSystemText_Invalid(t *testing.T) {
	p := &Personalization{UnitSystem: "invalid"}
	got := p.UnitSystemText()
	if got != "US Customary" {
		t.Errorf("UnitSystemText(invalid) = %q, want 'US Customary' (default)", got)
	}
}
