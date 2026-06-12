package service

import (
	"errors"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
)

func newCrudFamilyService(repo *testutil.MockFamilyRepo) *FamilyService {
	return NewFamilyService(&config.Config{}, repo, &testutil.MockTextProvider{})
}

// --- CreateFamily ---

func TestCreateFamily_Success(t *testing.T) {
	var created *models.Family
	repo := &testutil.MockFamilyRepo{
		CreateFamilyFunc: func(family *models.Family) error {
			family.ID = 7
			created = family
			return nil
		},
	}
	svc := newCrudFamilyService(repo)

	family, err := svc.CreateFamily(10, "The Does")
	if err != nil {
		t.Fatalf("CreateFamily error: %v", err)
	}
	if created == nil {
		t.Fatal("repo CreateFamily was not called")
	}
	if family.Name != "The Does" || family.OwnerID != 10 {
		t.Errorf("family = %+v, want Name='The Does' OwnerID=10", family)
	}
	if family.ID != 7 {
		t.Errorf("family.ID = %d, want repo-assigned 7", family.ID)
	}
}

func TestCreateFamily_RepoError(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		CreateFamilyFunc: func(family *models.Family) error {
			return errors.New("duplicate key value violates unique constraint")
		},
	}
	svc := newCrudFamilyService(repo)

	if _, err := svc.CreateFamily(10, "The Does"); err == nil {
		t.Fatal("expected error when repo create fails")
	}
}

// --- AddMember ---

func TestAddMember_Success(t *testing.T) {
	linkedUserID := uint(33)
	var created *models.FamilyMember
	repo := &testutil.MockFamilyRepo{
		CreateFamilyMemberFunc: func(member *models.FamilyMember) error {
			member.ID = 5
			created = member
			return nil
		},
	}
	svc := newCrudFamilyService(repo)

	member, err := svc.AddMember(7, "Joey", "son", &linkedUserID)
	if err != nil {
		t.Fatalf("AddMember error: %v", err)
	}
	if created == nil {
		t.Fatal("repo CreateFamilyMember was not called")
	}
	if member.FamilyID != 7 || member.Name != "Joey" || member.Relationship != "son" {
		t.Errorf("member = %+v, want FamilyID=7 Name=Joey Relationship=son", member)
	}
	if member.UserID == nil || *member.UserID != linkedUserID {
		t.Errorf("member.UserID = %v, want %d", member.UserID, linkedUserID)
	}
}

func TestAddMember_NilUserID(t *testing.T) {
	repo := &testutil.MockFamilyRepo{}
	svc := newCrudFamilyService(repo)

	member, err := svc.AddMember(7, "Grandma", "grandmother", nil)
	if err != nil {
		t.Fatalf("AddMember error: %v", err)
	}
	if member.UserID != nil {
		t.Errorf("member.UserID = %v, want nil for non-app members", member.UserID)
	}
}

func TestAddMember_RepoError(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		CreateFamilyMemberFunc: func(member *models.FamilyMember) error {
			return errors.New("db down")
		},
	}
	svc := newCrudFamilyService(repo)

	if _, err := svc.AddMember(7, "Joey", "son", nil); err == nil {
		t.Fatal("expected error when repo create fails")
	}
}

// --- VerifyMemberOwnership ---

func TestVerifyMemberOwnership_Owner(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	svc := newCrudFamilyService(repo)

	if err := svc.VerifyMemberOwnership(5, 10); err != nil {
		t.Errorf("VerifyMemberOwnership error for owner: %v", err)
	}
}

func TestVerifyMemberOwnership_NonOwner(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 99}, nil // belongs to someone else's family
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return &models.Family{ID: 7, OwnerID: ownerID}, nil
		},
	}
	svc := newCrudFamilyService(repo)

	if err := svc.VerifyMemberOwnership(5, 10); err == nil {
		t.Error("expected error when member belongs to a different family")
	}
}

func TestVerifyMemberOwnership_MemberNotFound(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return nil, errors.New("record not found")
		},
	}
	svc := newCrudFamilyService(repo)

	if err := svc.VerifyMemberOwnership(5, 10); err == nil {
		t.Error("expected error when member does not exist")
	}
}

func TestVerifyMemberOwnership_NoFamily(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7}, nil
		},
		GetFamilyByOwnerIDFunc: func(ownerID uint) (*models.Family, error) {
			return nil, errors.New("record not found")
		},
	}
	svc := newCrudFamilyService(repo)

	if err := svc.VerifyMemberOwnership(5, 10); err == nil {
		t.Error("expected error when the user has no family")
	}
}

// --- UpdateMember ---

func TestUpdateMember_Success(t *testing.T) {
	var updated *models.FamilyMember
	repo := &testutil.MockFamilyRepo{
		GetFamilyMemberByIDFunc: func(id uint) (*models.FamilyMember, error) {
			return &models.FamilyMember{ID: id, FamilyID: 7, Name: "Old", Relationship: "cousin"}, nil
		},
		UpdateFamilyMemberFunc: func(member *models.FamilyMember) error {
			updated = member
			return nil
		},
	}
	svc := newCrudFamilyService(repo)

	member, err := svc.UpdateMember(5, "New", "daughter")
	if err != nil {
		t.Fatalf("UpdateMember error: %v", err)
	}
	if updated == nil {
		t.Fatal("repo UpdateFamilyMember was not called")
	}
	if member.Name != "New" || member.Relationship != "daughter" {
		t.Errorf("member = %+v, want Name=New Relationship=daughter", member)
	}
	if member.ID != 5 || member.FamilyID != 7 {
		t.Errorf("member identity changed: ID=%d FamilyID=%d", member.ID, member.FamilyID)
	}
}

// --- UpdateDietaryProfile ---

func TestUpdateDietaryProfile_MergesIntoExistingRow(t *testing.T) {
	var saved *models.DietaryProfile
	repo := &testutil.MockFamilyRepo{
		GetOrCreateDietaryProfileFunc: func(memberID uint) (*models.DietaryProfile, error) {
			return &models.DietaryProfile{ID: 3, MemberID: memberID}, nil
		},
		UpdateDietaryProfileFunc: func(profile *models.DietaryProfile) error {
			saved = profile
			return nil
		},
	}
	svc := newCrudFamilyService(repo)

	incoming := &models.DietaryProfile{
		Allergies:    models.AllergyList{{Name: "peanuts", Severity: "severe", SubForms: []string{"peanut butter"}}},
		Intolerances: models.StringList{"lactose"},
		Restrictions: models.StringList{"halal"},
		Preferences:  models.StringList{"spicy"},
		MedicalNotes: "type 1 diabetes",
	}
	if err := svc.UpdateDietaryProfile(5, incoming); err != nil {
		t.Fatalf("UpdateDietaryProfile error: %v", err)
	}
	if saved == nil {
		t.Fatal("repo UpdateDietaryProfile was not called")
	}
	// The existing row identity is preserved; client-supplied IDs are ignored.
	if saved.ID != 3 || saved.MemberID != 5 {
		t.Errorf("saved identity ID=%d MemberID=%d, want 3/5", saved.ID, saved.MemberID)
	}
	if len(saved.Allergies) != 1 || saved.Allergies[0].Name != "peanuts" || len(saved.Allergies[0].SubForms) != 1 {
		t.Errorf("saved.Allergies = %+v, want incoming allergy with sub-forms", saved.Allergies)
	}
	if saved.MedicalNotes != "type 1 diabetes" {
		t.Errorf("saved.MedicalNotes = %q, want 'type 1 diabetes'", saved.MedicalNotes)
	}
	if len(saved.Intolerances) != 1 || len(saved.Restrictions) != 1 || len(saved.Preferences) != 1 {
		t.Errorf("saved lists = %v/%v/%v, want all copied", saved.Intolerances, saved.Restrictions, saved.Preferences)
	}
}

func TestUpdateDietaryProfile_GetOrCreateFails(t *testing.T) {
	repo := &testutil.MockFamilyRepo{
		GetOrCreateDietaryProfileFunc: func(memberID uint) (*models.DietaryProfile, error) {
			return nil, errors.New("db down")
		},
	}
	svc := newCrudFamilyService(repo)

	if err := svc.UpdateDietaryProfile(5, &models.DietaryProfile{}); err == nil {
		t.Fatal("expected error when profile lookup fails")
	}
}
