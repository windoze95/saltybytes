package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/config"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/repository"
)

// FamilyService is the business logic layer for family-related operations.
type FamilyService struct {
	Cfg        *config.Config
	Repo       *repository.FamilyRepository
	AIProvider ai.TextProvider
}

// NewFamilyService is the constructor function for initializing a new FamilyService.
func NewFamilyService(cfg *config.Config, repo *repository.FamilyRepository, aiProvider ai.TextProvider) *FamilyService {
	return &FamilyService{
		Cfg:        cfg,
		Repo:       repo,
		AIProvider: aiProvider,
	}
}

// CreateFamily creates a new family for the given owner.
func (s *FamilyService) CreateFamily(ownerID uint, name string) (*models.Family, error) {
	family := &models.Family{
		Name:    name,
		OwnerID: ownerID,
	}
	if err := s.Repo.CreateFamily(family); err != nil {
		return nil, fmt.Errorf("failed to create family: %w", err)
	}
	return family, nil
}

// GetFamily retrieves the family for the given owner.
func (s *FamilyService) GetFamily(ownerID uint) (*models.Family, error) {
	return s.Repo.GetFamilyByOwnerID(ownerID)
}

// AddMember adds a new member to a family.
func (s *FamilyService) AddMember(familyID uint, name, relationship string, userID *uint) (*models.FamilyMember, error) {
	member := &models.FamilyMember{
		FamilyID:     familyID,
		Name:         name,
		Relationship: relationship,
		UserID:       userID,
	}
	if err := s.Repo.CreateFamilyMember(member); err != nil {
		return nil, fmt.Errorf("failed to add family member: %w", err)
	}
	return member, nil
}

// VerifyMemberOwnership checks that the given user owns the family that the
// specified member belongs to. Returns an error if ownership cannot be verified.
func (s *FamilyService) VerifyMemberOwnership(memberID uint, userID uint) error {
	member, err := s.Repo.GetFamilyMemberByID(memberID)
	if err != nil {
		return fmt.Errorf("member not found: %w", err)
	}
	family, err := s.Repo.GetFamilyByOwnerID(userID)
	if err != nil {
		return errors.New("family not found")
	}
	if member.FamilyID != family.ID {
		return errors.New("unauthorized: you do not own this family member")
	}
	return nil
}

// UpdateMember updates an existing family member's name and relationship.
func (s *FamilyService) UpdateMember(memberID uint, name, relationship string) (*models.FamilyMember, error) {
	member, err := s.Repo.GetFamilyMemberByID(memberID)
	if err != nil {
		return nil, fmt.Errorf("failed to get family member: %w", err)
	}
	member.Name = name
	member.Relationship = relationship
	if err := s.Repo.UpdateFamilyMember(member); err != nil {
		return nil, fmt.Errorf("failed to update family member: %w", err)
	}
	return member, nil
}

// DeleteMember deletes a family member.
func (s *FamilyService) DeleteMember(memberID uint) error {
	return s.Repo.DeleteFamilyMember(memberID)
}

// UpdateDietaryProfile updates the dietary profile for a family member.
func (s *FamilyService) UpdateDietaryProfile(memberID uint, profile *models.DietaryProfile) error {
	existing, err := s.Repo.GetOrCreateDietaryProfile(memberID)
	if err != nil {
		return fmt.Errorf("failed to get dietary profile: %w", err)
	}

	existing.Allergies = profile.Allergies
	existing.Intolerances = profile.Intolerances
	existing.Restrictions = profile.Restrictions
	existing.Preferences = profile.Preferences
	existing.MedicalNotes = profile.MedicalNotes

	return s.Repo.UpdateDietaryProfile(existing)
}

// DietaryInterview conducts a multi-turn dietary interview for a family member.
func (s *FamilyService) DietaryInterview(ctx context.Context, memberID uint, messages []ai.Message) (string, error) {
	if s.AIProvider == nil {
		return "", errors.New("AI provider is not configured")
	}

	member, err := s.Repo.GetFamilyMemberByID(memberID)
	if err != nil {
		return "", fmt.Errorf("failed to get family member: %w", err)
	}

	return s.AIProvider.DietaryInterview(ctx, messages, member.Name)
}
