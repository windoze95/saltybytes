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

// --- IsValidUnitSystem ---

func TestIsValidUnitSystem_USCustomary(t *testing.T) {
	p := &Personalization{UnitSystem: USCustomary}
	if !p.IsValidUnitSystem() {
		t.Error("IsValidUnitSystem(USCustomary) should be true")
	}
}

func TestIsValidUnitSystem_Metric(t *testing.T) {
	p := &Personalization{UnitSystem: Metric}
	if !p.IsValidUnitSystem() {
		t.Error("IsValidUnitSystem(Metric) should be true")
	}
}

func TestIsValidUnitSystem_Invalid(t *testing.T) {
	p := &Personalization{UnitSystem: UnitSystem(99)}
	if p.IsValidUnitSystem() {
		t.Error("IsValidUnitSystem(99) should be false")
	}
}

// --- GetUnitSystemText ---

func TestGetUnitSystemText_USCustomary(t *testing.T) {
	p := &Personalization{UnitSystem: USCustomary}
	got := p.GetUnitSystemText()
	if got != USCustomaryText {
		t.Errorf("GetUnitSystemText(USCustomary) = %q, want %q", got, USCustomaryText)
	}
}

func TestGetUnitSystemText_Metric(t *testing.T) {
	p := &Personalization{UnitSystem: Metric}
	got := p.GetUnitSystemText()
	if got != MetricText {
		t.Errorf("GetUnitSystemText(Metric) = %q, want %q", got, MetricText)
	}
}

func TestGetUnitSystemText_Invalid(t *testing.T) {
	p := &Personalization{UnitSystem: UnitSystem(99)}
	got := p.GetUnitSystemText()
	if got != USCustomaryText {
		t.Errorf("GetUnitSystemText(99) = %q, want %q (default)", got, USCustomaryText)
	}
}
