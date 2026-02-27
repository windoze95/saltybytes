package repository

import (
	"github.com/windoze95/saltybytes-api/internal/logger"
	"github.com/windoze95/saltybytes-api/internal/models"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

// FamilyRepository is a repository for interacting with families.
type FamilyRepository struct {
	DB *gorm.DB
}

// NewFamilyRepository creates a new FamilyRepository.
func NewFamilyRepository(db *gorm.DB) *FamilyRepository {
	return &FamilyRepository{DB: db}
}

// CreateFamily creates a new family.
func (r *FamilyRepository) CreateFamily(family *models.Family) error {
	if err := r.DB.Create(family).Error; err != nil {
		logger.Get().Error("failed to create family", zap.Uint("owner_id", family.OwnerID), zap.Error(err))
		return err
	}
	return nil
}

// GetFamilyByOwnerID retrieves a family by its owner's user ID, preloading members and their dietary profiles.
func (r *FamilyRepository) GetFamilyByOwnerID(ownerID uint) (*models.Family, error) {
	var family models.Family
	if err := r.DB.Preload("Members.DietaryProfile").
		Where("owner_id = ?", ownerID).
		First(&family).Error; err != nil {
		return nil, err
	}
	return &family, nil
}

// CreateFamilyMember creates a new family member.
func (r *FamilyRepository) CreateFamilyMember(member *models.FamilyMember) error {
	if err := r.DB.Create(member).Error; err != nil {
		logger.Get().Error("failed to create family member", zap.Uint("family_id", member.FamilyID), zap.Error(err))
		return err
	}
	return nil
}

// GetFamilyMemberByID retrieves a family member by ID, preloading their dietary profile.
func (r *FamilyRepository) GetFamilyMemberByID(id uint) (*models.FamilyMember, error) {
	var member models.FamilyMember
	if err := r.DB.Preload("DietaryProfile").
		Where("id = ?", id).
		First(&member).Error; err != nil {
		return nil, err
	}
	return &member, nil
}

// UpdateFamilyMember updates an existing family member.
func (r *FamilyRepository) UpdateFamilyMember(member *models.FamilyMember) error {
	if err := r.DB.Save(member).Error; err != nil {
		logger.Get().Error("failed to update family member", zap.Uint("member_id", member.ID), zap.Error(err))
		return err
	}
	return nil
}

// DeleteFamilyMember soft-deletes a family member by ID.
func (r *FamilyRepository) DeleteFamilyMember(id uint) error {
	if err := r.DB.Delete(&models.FamilyMember{}, id).Error; err != nil {
		logger.Get().Error("failed to delete family member", zap.Uint("member_id", id), zap.Error(err))
		return err
	}
	return nil
}

// UpdateDietaryProfile updates an existing dietary profile.
func (r *FamilyRepository) UpdateDietaryProfile(profile *models.DietaryProfile) error {
	if err := r.DB.Save(profile).Error; err != nil {
		logger.Get().Error("failed to update dietary profile", zap.Uint("member_id", profile.MemberID), zap.Error(err))
		return err
	}
	return nil
}

// GetOrCreateDietaryProfile retrieves the dietary profile for a member, creating one if it doesn't exist.
func (r *FamilyRepository) GetOrCreateDietaryProfile(memberID uint) (*models.DietaryProfile, error) {
	var profile models.DietaryProfile
	err := r.DB.Where("member_id = ?", memberID).First(&profile).Error
	if err == nil {
		return &profile, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}

	profile = models.DietaryProfile{MemberID: memberID}
	if err := r.DB.Create(&profile).Error; err != nil {
		logger.Get().Error("failed to create dietary profile", zap.Uint("member_id", memberID), zap.Error(err))
		return nil, err
	}
	return &profile, nil
}
