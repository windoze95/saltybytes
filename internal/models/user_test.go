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
	s := &Subscription{Tier: TierFree, AllergenAnalysesUsed: 4}
	if !s.CanUseAllergenAnalysis() {
		t.Error("CanUseAllergenAnalysis: free tier with 4 uses should be true")
	}
}

func TestCanUseAllergenAnalysis_Free_AtLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AllergenAnalysesUsed: 5}
	if s.CanUseAllergenAnalysis() {
		t.Error("CanUseAllergenAnalysis: free tier with 5 uses should be false")
	}
}

func TestCanUseAllergenAnalysis_Premium(t *testing.T) {
	s := &Subscription{Tier: TierPremium, AllergenAnalysesUsed: 100}
	if !s.CanUseAllergenAnalysis() {
		t.Error("CanUseAllergenAnalysis: premium should always be true")
	}
}

func TestCanUseWebSearch_Free_UnderLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, WebSearchesUsed: 19}
	if !s.CanUseWebSearch() {
		t.Error("CanUseWebSearch: free tier with 19 uses should be true")
	}
}

func TestCanUseWebSearch_Free_AtLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, WebSearchesUsed: 20}
	if s.CanUseWebSearch() {
		t.Error("CanUseWebSearch: free tier with 20 uses should be false")
	}
}

func TestCanUseWebSearch_Premium(t *testing.T) {
	s := &Subscription{Tier: TierPremium, WebSearchesUsed: 1000}
	if !s.CanUseWebSearch() {
		t.Error("CanUseWebSearch: premium should always be true")
	}
}

func TestCanUseAIGeneration_Free_UnderLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AIGenerationsUsed: 49}
	if !s.CanUseAIGeneration() {
		t.Error("CanUseAIGeneration: free tier with 49 uses should be true")
	}
}

func TestCanUseAIGeneration_Free_AtLimit(t *testing.T) {
	s := &Subscription{Tier: TierFree, AIGenerationsUsed: 50}
	if s.CanUseAIGeneration() {
		t.Error("CanUseAIGeneration: free tier with 50 uses should be false")
	}
}

func TestCanUseAIGeneration_Premium(t *testing.T) {
	s := &Subscription{Tier: TierPremium, AIGenerationsUsed: 9999}
	if !s.CanUseAIGeneration() {
		t.Error("CanUseAIGeneration: premium should always be true")
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
