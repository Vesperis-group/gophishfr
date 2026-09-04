package models

import (
	"crypto/hmac"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Vesperis-group/gophishfr/internal/apikey"
	log "github.com/Vesperis-group/gophishfr/logger"
	"github.com/jinzhu/gorm"
)

// ErrModifyingOnlyAdmin occurs when there is an attempt to modify the only
// user account with the Admin role in such a way that there will be no user
// accounts left in GophishFR with that role.
var ErrModifyingOnlyAdmin = errors.New("Cannot remove the only administrator")

// User represents the user model for GophishFR.
type User struct {
	Id                     int64     `json:"id"`
	Username               string    `json:"username" sql:"not null;unique"`
	Hash                   string    `json:"-"`
	APIKeyVerifier         []byte    `json:"-" gorm:"column:api_key_verifier"`
	APIKeyVerifierKeyID    string    `json:"-" gorm:"column:api_key_verifier_key_id"`
	Role                   Role      `json:"role" gorm:"association_autoupdate:false;association_autocreate:false"`
	RoleID                 int64     `json:"-"`
	PasswordChangeRequired bool      `json:"password_change_required"`
	AccountLocked          bool      `json:"account_locked"`
	LastLogin              time.Time `json:"last_login"`
}

const runtimeUserColumns = "users.id, users.username, users.hash, users.role_id, " +
	"users.password_change_required, users.last_login, users.account_locked, " +
	"users.api_key_verifier, users.api_key_verifier_key_id"

var (
	apiKeyVerifierMu sync.RWMutex
	apiKeyVerifier   *apikey.Service
)

var (
	ErrAPIKeyVerifierUnavailable = errors.New("API verifier key unavailable")
	ErrInvalidAPIKeyState        = errors.New("invalid API verifier state")
	ErrInvalidAPICredential      = errors.New("invalid API credential")
	ErrAmbiguousAPICredential    = errors.New("ambiguous API credential")
)

// SetAPIKeyVerifier installs immutable verifier state loaded once by startup.
// It is also an explicit injection boundary for isolated tests.
func SetAPIKeyVerifier(verifier *apikey.Service) {
	apiKeyVerifierMu.Lock()
	apiKeyVerifier = verifier
	apiKeyVerifierMu.Unlock()
}

func currentAPIKeyVerifier() (*apikey.Service, error) {
	apiKeyVerifierMu.RLock()
	verifier := apiKeyVerifier
	apiKeyVerifierMu.RUnlock()
	if verifier == nil {
		return nil, ErrAPIKeyVerifierUnavailable
	}
	return verifier, nil
}

// GetUser returns the user that the given id corresponds to. If no user is found, an
// error is thrown.
func GetUser(id int64) (User, error) {
	u := User{}
	err := db.Select(runtimeUserColumns).Preload("Role").Where("users.id=?", id).First(&u).Error
	return u, err
}

// GetUsers returns the users registered in GophishFR.
func GetUsers() ([]User, error) {
	us := []User{}
	err := db.Select(runtimeUserColumns).Preload("Role").Find(&us).Error
	return us, err
}

// GetUserByAPIKey authenticates through one bounded indexed verifier lookup.
// It never reads or falls back to the legacy plaintext column.
func GetUserByAPIKey(token string) (User, error) {
	verifier, err := currentAPIKeyVerifier()
	if err != nil {
		return User{}, err
	}
	candidates, err := verifier.Candidates([]byte(token))
	if err != nil {
		return User{}, ErrInvalidAPICredential
	}
	query, arguments := candidateWhere(candidates)
	users := make([]User, 0, 2)
	err = db.Select(runtimeUserColumns).Preload("Role").
		Where("api_key IS NULL AND (account_locked IS NULL OR account_locked = ?)", false).
		Where(query, arguments...).Limit(2).Find(&users).Error
	if err != nil {
		return User{}, err
	}
	if len(users) == 0 {
		return User{}, ErrInvalidAPICredential
	}
	if len(users) != 1 {
		return User{}, ErrAmbiguousAPICredential
	}
	user := users[0]
	if user.AccountLocked {
		return User{}, ErrInvalidAPICredential
	}
	if err := validateMigratedAPIKeyState(user.APIKeyVerifierKeyID, user.APIKeyVerifier); err != nil {
		return User{}, err
	}
	if err := verifier.Verify(user.APIKeyVerifierKeyID, []byte(token), user.APIKeyVerifier); err != nil {
		return User{}, ErrInvalidAPICredential
	}
	if user.APIKeyVerifierKeyID != verifier.ActiveKeyID() {
		if err := lazyUpgradeAPIKeyVerifier(&user, []byte(token), verifier); err != nil {
			return User{}, err
		}
	}
	// This final state check is the authentication linearization point. A lock
	// committed before it rejects this request; a later lock applies to
	// subsequent requests without changing session behavior.
	if err := ensureAPIKeyAccountUnlocked(user.Id); err != nil {
		return User{}, err
	}
	return user, nil
}

// GetUserByUsername returns the user that the given username corresponds to. If no user is found, an
// error is thrown.
func GetUserByUsername(username string) (User, error) {
	u := User{}
	err := db.Select(runtimeUserColumns).Preload("Role").Where("users.username = ?", username).First(&u).Error
	return u, err
}

// PutUser updates ordinary mutable user fields. API-key state is deliberately
// excluded: only the dedicated issuance, reset, lazy-rekey, and migration paths
// may write those columns.
func PutUser(u *User) error {
	if u.Id == 0 {
		return ErrInvalidAPIKeyState
	}
	result := db.Model(&User{}).Where("id = ?", u.Id).Updates(map[string]interface{}{
		"username":                 u.Username,
		"hash":                     u.Hash,
		"role_id":                  u.RoleID,
		"password_change_required": u.PasswordChangeRequired,
		"account_locked":           u.AccountLocked,
		"last_login":               u.LastLogin,
	})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		var count int
		if err := db.Model(&User{}).Where("id = ?", u.Id).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return gorm.ErrRecordNotFound
		}
	}
	return nil
}

// CreateUserWithAPIKey creates a verifier-only user and returns its generated
// client token to the immediate caller exactly once.
func CreateUserWithAPIKey(u *User) (string, error) {
	verifier, err := currentAPIKeyVerifier()
	if err != nil {
		return "", err
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		return "", transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	token, err := generateUniqueAPIKey(transaction, verifier)
	if err != nil {
		return "", err
	}
	if err := assignVerifier(u, []byte(token), verifier); err != nil {
		return "", err
	}
	if err := transaction.Create(u).Error; err != nil {
		return "", err
	}
	if err := verifyAPIKeyWrite(transaction, u.Id, u.APIKeyVerifierKeyID, u.APIKeyVerifier); err != nil {
		return "", err
	}
	if err := transaction.Commit().Error; err != nil {
		return "", err
	}
	committed = true
	return token, nil
}

// ResetUserAPIKey atomically replaces one user's verifier and returns the new
// token only after the replacement commits.
func ResetUserAPIKey(id int64) (string, error) {
	verifier, err := currentAPIKeyVerifier()
	if err != nil {
		return "", err
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		return "", transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	token, err := generateUniqueAPIKey(transaction, verifier)
	if err != nil {
		return "", err
	}
	keyID, digest, err := verifier.ComputeActive([]byte(token))
	if err != nil {
		return "", ErrAPIKeyVerifierUnavailable
	}
	result := transaction.Model(&User{}).Where("id = ?", id).Updates(map[string]interface{}{
		"api_key":                 nil,
		"api_key_verifier":        digest[:],
		"api_key_verifier_key_id": keyID,
	})
	if result.Error != nil || result.RowsAffected != 1 {
		if result.Error != nil {
			return "", result.Error
		}
		return "", gorm.ErrRecordNotFound
	}
	if err := verifyAPIKeyWrite(transaction, id, keyID, digest[:]); err != nil {
		return "", err
	}
	if err := transaction.Commit().Error; err != nil {
		return "", err
	}
	committed = true
	return token, nil
}

func createUserWithToken(u *User, token []byte) error {
	verifier, err := currentAPIKeyVerifier()
	if err != nil {
		return err
	}
	transaction := db.Begin()
	if transaction.Error != nil {
		return transaction.Error
	}
	committed := false
	defer func() {
		if !committed {
			transaction.Rollback()
		}
	}()
	if collision, err := tokenCollides(transaction, verifier, token); err != nil {
		return err
	} else if collision {
		return ErrAmbiguousAPICredential
	}
	if err := assignVerifier(u, token, verifier); err != nil {
		return err
	}
	if err := transaction.Create(u).Error; err != nil {
		return err
	}
	if err := verifyAPIKeyWrite(transaction, u.Id, u.APIKeyVerifierKeyID, u.APIKeyVerifier); err != nil {
		return err
	}
	if err := transaction.Commit().Error; err != nil {
		return err
	}
	committed = true
	return nil
}

func assignVerifier(user *User, token []byte, verifier *apikey.Service) error {
	keyID, digest, err := verifier.ComputeActive(token)
	if err != nil {
		return ErrAPIKeyVerifierUnavailable
	}
	user.APIKeyVerifierKeyID = keyID
	user.APIKeyVerifier = append(user.APIKeyVerifier[:0], digest[:]...)
	return nil
}

func generateUniqueAPIKey(transaction *gorm.DB, verifier *apikey.Service) (string, error) {
	for attempt := 0; attempt < 5; attempt++ {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			return "", errors.New("generate API credential")
		}
		token := hex.EncodeToString(raw)
		collision, err := tokenCollides(transaction, verifier, []byte(token))
		if err != nil {
			return "", err
		}
		if !collision {
			return token, nil
		}
	}
	return "", errors.New("unable to generate a unique API credential")
}

func tokenCollides(transaction *gorm.DB, verifier *apikey.Service, token []byte) (bool, error) {
	candidates, err := verifier.Candidates(token)
	if err != nil {
		return false, err
	}
	query, arguments := candidateWhere(candidates)
	var count int
	err = transaction.Model(&User{}).Where(query, arguments...).Count(&count).Error
	return count != 0, err
}

func candidateWhere(candidates []apikey.Candidate) (string, []interface{}) {
	clauses := make([]string, 0, len(candidates))
	arguments := make([]interface{}, 0, len(candidates)*2)
	for _, candidate := range candidates {
		clauses = append(clauses, "(api_key_verifier_key_id = ? AND api_key_verifier = ?)")
		arguments = append(arguments, candidate.KeyID, candidate.Verifier[:])
	}
	return strings.Join(clauses, " OR "), arguments
}

func validateMigratedAPIKeyState(keyID string, verifier []byte) error {
	if !apikey.ValidKeyID(keyID) || len(verifier) != apikey.VerifierSize {
		return ErrInvalidAPIKeyState
	}
	return nil
}

func verifyAPIKeyWrite(transaction *gorm.DB, id int64, keyID string, verifier []byte) error {
	var stored struct {
		Verifier      []byte  `gorm:"column:api_key_verifier"`
		VerifierKeyID *string `gorm:"column:api_key_verifier_key_id"`
	}
	if err := transaction.Raw(
		"SELECT api_key_verifier, api_key_verifier_key_id FROM users "+
			"WHERE id = ? AND api_key IS NULL", id,
	).Scan(&stored).Error; err != nil {
		return err
	}
	if stored.VerifierKeyID == nil ||
		*stored.VerifierKeyID != keyID || !hmacEqual(verifier, stored.Verifier) {
		return ErrInvalidAPIKeyState
	}
	return nil
}

func hmacEqual(left, right []byte) bool {
	return len(left) == apikey.VerifierSize && len(right) == apikey.VerifierSize &&
		hmac.Equal(left, right)
}

func ensureAPIKeyAccountUnlocked(id int64) error {
	var count int
	if err := db.Model(&User{}).
		Where("id = ? AND (account_locked IS NULL OR account_locked = ?)", id, false).
		Count(&count).Error; err != nil {
		return err
	}
	if count != 1 {
		return ErrInvalidAPICredential
	}
	return nil
}

func lazyUpgradeAPIKeyVerifier(user *User, token []byte, verifier *apikey.Service) error {
	activeID, activeVerifier, err := verifier.ComputeActive(token)
	if err != nil {
		return ErrAPIKeyVerifierUnavailable
	}
	result := db.Model(&User{}).
		Where("id = ? AND api_key IS NULL AND (account_locked IS NULL OR account_locked = ?) "+
			"AND api_key_verifier_key_id = ? AND api_key_verifier = ?",
			user.Id, false, user.APIKeyVerifierKeyID, user.APIKeyVerifier).
		Updates(map[string]interface{}{
			"api_key_verifier_key_id": activeID,
			"api_key_verifier":        activeVerifier[:],
		})
	if result.Error != nil {
		// Rekey is best-effort after successful verification. Leave the accepted
		// old verifier in place without exposing any secret-derived value.
		log.Warnf("Unable to lazily upgrade API verifier for user ID %d", user.Id)
		return nil
	}
	if result.RowsAffected == 1 {
		user.APIKeyVerifierKeyID = activeID
		user.APIKeyVerifier = append(user.APIKeyVerifier[:0], activeVerifier[:]...)
		return nil
	}

	var current struct {
		Verifier      []byte  `gorm:"column:api_key_verifier"`
		VerifierKeyID *string `gorm:"column:api_key_verifier_key_id"`
	}
	if err := db.Raw(
		"SELECT api_key_verifier, api_key_verifier_key_id FROM users "+
			"WHERE id = ? AND api_key IS NULL "+
			"AND (account_locked IS NULL OR account_locked = ?)", user.Id, false,
	).Scan(&current).Error; err != nil {
		return ErrInvalidAPICredential
	}
	if current.VerifierKeyID == nil ||
		validateMigratedAPIKeyState(*current.VerifierKeyID, current.Verifier) != nil ||
		verifier.Verify(*current.VerifierKeyID, token, current.Verifier) != nil {
		return ErrInvalidAPICredential
	}
	user.APIKeyVerifierKeyID = *current.VerifierKeyID
	user.APIKeyVerifier = append(user.APIKeyVerifier[:0], current.Verifier...)
	return nil
}

// EnsureEnoughAdmins ensures that there is more than one user account in
// GophishFR with the Admin role. This function is meant to be called before
// modifying a user account with the Admin role in a non-revokable way.
func EnsureEnoughAdmins() error {
	role, err := GetRoleBySlug(RoleAdmin)
	if err != nil {
		return err
	}
	var adminCount int
	err = db.Model(&User{}).Where("role_id=?", role.ID).Count(&adminCount).Error
	if err != nil {
		return err
	}
	if adminCount == 1 {
		return ErrModifyingOnlyAdmin
	}
	return nil
}

// DeleteUser deletes the given user. To ensure that there is always at least
// one user account with the Admin role, this function will refuse to delete
// the last Admin.
func DeleteUser(id int64) error {
	existing, err := GetUser(id)
	if err != nil {
		return err
	}
	// If the user is an admin, we need to verify that it's not the last one.
	if existing.Role.Slug == RoleAdmin {
		err = EnsureEnoughAdmins()
		if err != nil {
			return err
		}
	}
	campaigns, err := GetCampaigns(id)
	if err != nil {
		return err
	}
	// Delete the campaigns
	log.Infof("Deleting campaigns for user ID %d", id)
	for _, campaign := range campaigns {
		err = DeleteCampaign(campaign.Id)
		if err != nil {
			return err
		}
	}
	log.Infof("Deleting pages for user ID %d", id)
	// Delete the landing pages
	pages, err := GetPages(id)
	if err != nil {
		return err
	}
	for _, page := range pages {
		err = DeletePage(page.Id, id)
		if err != nil {
			return err
		}
	}
	// Delete the templates
	log.Infof("Deleting templates for user ID %d", id)
	templates, err := GetTemplates(id)
	if err != nil {
		return err
	}
	for _, template := range templates {
		err = DeleteTemplate(template.Id, id)
		if err != nil {
			return err
		}
	}
	// Delete the groups
	log.Infof("Deleting groups for user ID %d", id)
	groups, err := GetGroups(id)
	if err != nil {
		return err
	}
	for _, group := range groups {
		err = DeleteGroup(&group)
		if err != nil {
			return err
		}
	}
	// Delete the sending profiles
	log.Infof("Deleting sending profiles for user ID %d", id)
	profiles, err := GetSMTPs(id)
	if err != nil {
		return err
	}
	for _, profile := range profiles {
		err = DeleteSMTP(profile.Id, id)
		if err != nil {
			return err
		}
	}
	// Finally, delete the user
	err = db.Where("id=?", id).Delete(&User{}).Error
	return err
}
