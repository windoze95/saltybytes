package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/windoze95/saltybytes-api/internal/ai"
	"github.com/windoze95/saltybytes-api/internal/models"
	"github.com/windoze95/saltybytes-api/internal/testutil"
	"gorm.io/gorm"
)

func TestFinderSessionService_SaveListGetDelete(t *testing.T) {
	repo := testutil.NewMockFinderSessionRepo()
	svc := NewFinderSessionService(repo)
	ctx := context.Background()

	// Two sessions for user 1, one for user 2.
	for i := 0; i < 2; i++ {
		if err := svc.Save(ctx, &models.FinderSession{UserID: 1, Title: fmt.Sprintf("s%d", i)}); err != nil {
			t.Fatalf("Save: %v", err)
		}
	}
	if err := svc.Save(ctx, &models.FinderSession{UserID: 2, Title: "other"}); err != nil {
		t.Fatalf("Save other: %v", err)
	}

	// List returns only user 1's, newest first, with the correct total.
	list, total, err := svc.List(ctx, 1, 10, 0)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("List returned %d (total %d), want 2 (total 2)", len(list), total)
	}
	if list[0].Title != "s1" {
		t.Errorf("newest-first: list[0].Title = %q, want s1", list[0].Title)
	}

	// Get enforces ownership.
	got, err := svc.Get(ctx, 1, list[0].ID)
	if err != nil {
		t.Fatalf("Get own: %v", err)
	}
	if got.ID != list[0].ID {
		t.Errorf("Get returned id %d, want %d", got.ID, list[0].ID)
	}
	if _, err := svc.Get(ctx, 999, list[0].ID); !errors.Is(err, ErrFinderSessionNotOwned) {
		t.Errorf("Get by non-owner err = %v, want ErrFinderSessionNotOwned", err)
	}

	// Delete enforces ownership, then removes.
	if err := svc.Delete(ctx, 999, list[0].ID); !errors.Is(err, ErrFinderSessionNotOwned) {
		t.Errorf("Delete by non-owner err = %v, want ErrFinderSessionNotOwned", err)
	}
	if err := svc.Delete(ctx, 1, list[0].ID); err != nil {
		t.Fatalf("Delete own: %v", err)
	}
	if _, err := svc.Get(ctx, 1, list[0].ID); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Errorf("after delete, Get err = %v, want ErrRecordNotFound", err)
	}
}

func TestFindRecipes_AutoSavesCompletedRun(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, nil) // rank all, flag none
	repo := testutil.NewMockFinderSessionRepo()

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.Sessions = NewFinderSessionService(repo)

	_ = runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken", Occasion: "dinner"}})

	created := repo.Created()
	if len(created) != 1 {
		t.Fatalf("auto-save created %d sessions, want exactly 1", len(created))
	}
	sess := created[0]
	if sess.UserID != testutil.TestUser().ID {
		t.Errorf("session UserID = %d, want %d", sess.UserID, testutil.TestUser().ID)
	}
	if len(sess.Results) != 3 {
		t.Errorf("session stored %d results, want 3 (the shortlist)", len(sess.Results))
	}
	if sess.Title == "" {
		t.Errorf("session has an empty title")
	}
	if sess.Intent.Protein != "chicken" {
		t.Errorf("session intent protein = %q, want chicken", sess.Intent.Protein)
	}
	if len(sess.Narration) == 0 {
		t.Errorf("session has no narration")
	}
}

func TestFindRecipes_NoAutoSaveOnPagination(t *testing.T) {
	results := digSearchResults(3)
	searchProvider := &testutil.MockSearchProvider{
		SearchRecipesFunc: func(ctx context.Context, query string, count, offset int) ([]ai.SearchResult, error) {
			return results, nil
		},
	}
	ranker := rankAllFlagging(results, nil)
	repo := testutil.NewMockFinderSessionRepo()

	svc := newFinderService(searchProvider, ranker, &testutil.MockFamilyRepo{})
	svc.Sessions = NewFinderSessionService(repo)

	// A paginated (offset > 0) run must NOT create a session.
	_ = runFinder(svc, testutil.TestUser(), FinderRequest{Facets: FinderFacets{Protein: "chicken"}, Offset: 10})

	if n := len(repo.Created()); n != 0 {
		t.Errorf("paginated run created %d sessions, want 0", n)
	}
}
