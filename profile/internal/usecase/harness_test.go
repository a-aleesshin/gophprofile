package usecase

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/a-aleesshin/gophprofile/profile/internal/domain"
)

var testNow = time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)

type fixedClock struct {
	now time.Time
}

func (c fixedClock) Now() time.Time { return c.now }

type stubTransactor struct {
	err    error
	inside bool
	calls  int
}

func (s *stubTransactor) InTx(ctx context.Context, fn func(context.Context) error) error {
	s.calls++
	s.inside = true

	defer func() { s.inside = false }()

	if err := fn(ctx); err != nil {
		return err
	}

	return s.err
}

type stubPolicies struct {
	policy UploadPolicy
}

func (s stubPolicies) UploadPolicy() UploadPolicy { return s.policy }

type stubProfiles struct {
	profiles map[string]domain.Profile
	byIDErr  error
	saveErr  error
	created  []domain.Profile
	saved    []domain.Profile

	createErr  error
	raceWinner *domain.Profile
}

func (s *stubProfiles) ByUserID(_ context.Context, userID domain.UserID) (domain.Profile, error) {
	if s.byIDErr != nil {
		return domain.Profile{}, s.byIDErr
	}

	profile, found := s.profiles[userID.String()]
	if !found {
		return domain.Profile{}, domain.ErrProfileNotFound
	}

	return profile, nil
}

func (s *stubProfiles) Create(_ context.Context, profile *domain.Profile) error {
	s.created = append(s.created, *profile)

	if s.createErr == nil {
		return s.saveErr
	}

	if s.raceWinner != nil {
		s.profiles[profile.UserID().String()] = *s.raceWinner
	}

	return s.createErr
}

func (s *stubProfiles) Save(_ context.Context, profile *domain.Profile) error {
	s.saved = append(s.saved, *profile)

	return s.saveErr
}

type stubAvatars struct {
	avatars  map[string]domain.Avatar
	byFile   map[string]domain.Avatar
	count    int
	countErr error
	byIDErr  error
	saveErr  error
	created  []domain.Avatar
	saved    []domain.Avatar

	stale       []domain.Avatar
	staleErr    error
	staleStatus domain.AvatarStatus
	staleBefore time.Time
	staleLimit  int
}

func (s *stubAvatars) ByID(_ context.Context, id domain.AvatarID) (domain.Avatar, error) {
	if s.byIDErr != nil {
		return domain.Avatar{}, s.byIDErr
	}

	avatar, found := s.avatars[id.String()]
	if !found {
		return domain.Avatar{}, domain.ErrAvatarNotFound
	}

	return avatar, nil
}

func (s *stubAvatars) ByFileID(_ context.Context, fileID domain.FileRef) (domain.Avatar, error) {
	if s.byIDErr != nil {
		return domain.Avatar{}, s.byIDErr
	}

	avatar, found := s.byFile[fileID.String()]
	if !found {
		return domain.Avatar{}, domain.ErrAvatarNotFound
	}

	return avatar, nil
}

func (s *stubAvatars) Create(_ context.Context, avatar *domain.Avatar) error {
	s.created = append(s.created, *avatar)

	return s.saveErr
}

func (s *stubAvatars) Save(_ context.Context, avatar *domain.Avatar) error {
	s.saved = append(s.saved, *avatar)

	return s.saveErr
}

func (s *stubAvatars) CountByUser(context.Context, domain.UserID) (int, error) {
	return s.count, s.countErr
}

func (s *stubAvatars) StaleByStatus(
	_ context.Context,
	status domain.AvatarStatus,
	updatedBefore time.Time,
	limit int,
) ([]domain.Avatar, error) {
	s.staleStatus = status
	s.staleBefore = updatedBefore
	s.staleLimit = limit

	if s.staleErr != nil {
		return nil, s.staleErr
	}

	return s.stale, nil
}

type stubTickets struct {
	params CreateUploadParams
	ticket CreateUploadTicket
	err    error
	calls  int
}

func (s *stubTickets) CreateUpload(
	_ context.Context,
	params CreateUploadParams,
) (CreateUploadTicket, error) {
	s.params = params
	s.calls++

	if s.err != nil {
		return CreateUploadTicket{}, s.err
	}

	return s.ticket, nil
}

type stubConfirmer struct {
	uploadID     string
	confirmation CompleteUploadConfirmation
	err          error
	calls        int
}

func (s *stubConfirmer) CompleteUpload(
	_ context.Context,
	uploadID string,
) (CompleteUploadConfirmation, error) {
	s.uploadID = uploadID
	s.calls++

	if s.err != nil {
		return CompleteUploadConfirmation{}, s.err
	}

	return s.confirmation, nil
}

type stubFactPublisher struct {
	facts  []AvatarDeletedFact
	err    error
	inTx   bool
	keeper *stubTransactor
}

func (s *stubFactPublisher) PublishAvatarDeleted(_ context.Context, fact AvatarDeletedFact) error {
	if s.keeper != nil && s.keeper.inside {
		s.inTx = true
	}

	s.facts = append(s.facts, fact)

	return s.err
}

func testUserID(t *testing.T, raw string) domain.UserID {
	t.Helper()

	value, err := domain.NewUserID(raw)
	require.NoError(t, err)

	return value
}

func testUploadRef(t *testing.T) domain.UploadRef {
	t.Helper()

	value, err := domain.ParseUploadRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testFileRef(t *testing.T) domain.FileRef {
	t.Helper()

	value, err := domain.ParseFileRef(uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)

	return value
}

func testSnapshot(t *testing.T) domain.FileSnapshot {
	t.Helper()

	value, err := domain.NewFileSnapshot(domain.NewFileSnapshotParams{
		FileName:  "avatar.jpg",
		MimeType:  "image/jpeg",
		SizeBytes: 2048,
		Width:     1920,
		Height:    1080,
	})
	require.NoError(t, err)

	return value
}

func testVariants(t *testing.T) []domain.Variant {
	t.Helper()

	variant, err := domain.NewVariant(domain.NewVariantParams{
		Name: "300x300", Ext: "jpg", MimeType: "image/jpeg", Width: 300, Height: 300,
	})
	require.NoError(t, err)

	return []domain.Variant{variant}
}

func testPendingAvatar(t *testing.T, user domain.UserID) domain.Avatar {
	t.Helper()

	id, err := domain.NewAvatarID()
	require.NoError(t, err)

	avatar, err := domain.NewAvatar(domain.NewAvatarParams{
		ID:       id,
		UserID:   user,
		UploadID: testUploadRef(t),
		Now:      testNow,
	})
	require.NoError(t, err)

	return avatar
}

func testProcessingAvatar(t *testing.T, user domain.UserID) domain.Avatar {
	t.Helper()

	avatar := testPendingAvatar(t, user)
	require.NoError(t, avatar.StartProcessing(testFileRef(t), testNow))

	return avatar
}

func testUploadPolicy() UploadPolicy {
	return UploadPolicy{
		MaxSizeBytes:      10 << 20,
		AllowedMime:       []string{"image/jpeg", "image/png", "image/webp"},
		MaxAvatarsPerUser: 20,
	}
}

type stubViews struct {
	views      map[string]AvatarView
	byUser     []AvatarView
	page       Page
	byIDErr    error
	listErr    error
	byHash     map[string]domain.Profile
	hashErr    error
	profileErr error
	profiles   map[string]domain.Profile
}

func (s *stubViews) ViewByID(_ context.Context, id domain.AvatarID) (AvatarView, error) {
	if s.byIDErr != nil {
		return AvatarView{}, s.byIDErr
	}

	view, found := s.views[id.String()]
	if !found {
		return AvatarView{}, domain.ErrAvatarNotFound
	}

	return view, nil
}

func (s *stubViews) ViewsByUser(
	_ context.Context,
	_ domain.UserID,
	page Page,
) ([]AvatarView, error) {
	s.page = page

	if s.listErr != nil {
		return nil, s.listErr
	}

	if page.Limit < len(s.byUser) {
		return s.byUser[:page.Limit], nil
	}

	return s.byUser, nil
}

func (s *stubViews) ByUserID(_ context.Context, userID domain.UserID) (domain.Profile, error) {
	if s.profileErr != nil {
		return domain.Profile{}, s.profileErr
	}

	profile, found := s.profiles[userID.String()]
	if !found {
		return domain.Profile{}, domain.ErrProfileNotFound
	}

	return profile, nil
}

func (s *stubViews) ByEmailHash(
	_ context.Context,
	hash domain.EmailHash,
) (domain.Profile, error) {
	if s.hashErr != nil {
		return domain.Profile{}, s.hashErr
	}

	profile, found := s.byHash[hash.String()]
	if !found {
		return domain.Profile{}, domain.ErrProfileNotFound
	}

	return profile, nil
}

type stubURLs struct {
	base        string
	placeholder string
	err         error
}

func (s stubURLs) VariantURL(fileID domain.FileRef, name, ext string) (string, error) {
	if s.err != nil {
		return "", s.err
	}

	return s.base + "/upload/" + fileID.String() + "/" + name + "." + ext, nil
}

func (s stubURLs) PlaceholderURL() string { return s.placeholder }

func testURLs() stubURLs {
	return stubURLs{
		base:        "https://domain.com",
		placeholder: "https://domain.com/static/avatar-placeholder.png",
	}
}

func testReadyView(t *testing.T, user domain.UserID, created time.Time) AvatarView {
	t.Helper()

	id, err := domain.NewAvatarID()
	require.NoError(t, err)

	return AvatarView{
		ID:        id,
		UserID:    user,
		FileID:    testFileRef(t),
		Status:    domain.AvatarStatusReady,
		FileName:  "avatar.jpg",
		MimeType:  "image/jpeg",
		SizeBytes: 2048,
		Width:     1920,
		Height:    1080,
		Variants: []VariantView{
			{Name: "100x100", Ext: "jpg", MimeType: "image/jpeg", Width: 100, Height: 100},
			{Name: "300x300", Ext: "jpg", MimeType: "image/jpeg", Width: 300, Height: 300},
			{Name: "web", Ext: "jpg", MimeType: "image/jpeg", Width: 800, Height: 600},
		},
		CreatedAt: created,
		UpdatedAt: created,
	}
}

func testEmailHash(t *testing.T) domain.EmailHash {
	t.Helper()

	email, err := domain.NewEmail("someone@example.com")
	require.NoError(t, err)

	return email.Hash()
}
