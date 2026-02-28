package service

import (
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jeremy/longbox/internal/model"
	"github.com/jeremy/longbox/internal/repository"
	"golang.org/x/crypto/bcrypt"
)

type AuthService struct {
	userRepo        *repository.UserRepo
	sessionLifetime time.Duration
}

func NewAuthService(userRepo *repository.UserRepo, sessionLifetime time.Duration) *AuthService {
	return &AuthService{
		userRepo:        userRepo,
		sessionLifetime: sessionLifetime,
	}
}

func (s *AuthService) AuthEnabled() (bool, error) {
	count, err := s.userRepo.Count()
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func (s *AuthService) Register(username, password string) (*model.User, error) {
	if username == "" || password == "" {
		return nil, fmt.Errorf("username and password are required")
	}
	if len(password) < 8 {
		return nil, fmt.Errorf("password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, fmt.Errorf("hashing password: %w", err)
	}

	user := &model.User{
		Username:     username,
		PasswordHash: string(hash),
	}

	if err := s.userRepo.CreateIfAllowed(user); err != nil {
		return nil, err
	}

	return user, nil
}

func (s *AuthService) Login(username, password string) (*model.User, *model.Session, error) {
	user, err := s.userRepo.GetByUsername(username)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up user: %w", err)
	}
	if user == nil {
		return nil, nil, fmt.Errorf("invalid credentials")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)); err != nil {
		return nil, nil, fmt.Errorf("invalid credentials")
	}

	session := &model.Session{
		Token:     uuid.New().String(),
		UserID:    user.ID,
		ExpiresAt: time.Now().UTC().Add(s.sessionLifetime),
	}

	if err := s.userRepo.CreateSession(session); err != nil {
		return nil, nil, fmt.Errorf("creating session: %w", err)
	}

	return user, session, nil
}

func (s *AuthService) Logout(token string) error {
	return s.userRepo.DeleteSession(token)
}

func (s *AuthService) ValidateSession(token string) (*model.User, error) {
	session, err := s.userRepo.GetSession(token)
	if err != nil {
		return nil, err
	}
	if session == nil {
		return nil, nil
	}

	if time.Now().UTC().After(session.ExpiresAt) {
		_ = s.userRepo.DeleteSession(token)
		return nil, nil
	}

	user, err := s.userRepo.GetByID(session.UserID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (s *AuthService) ListUsers() ([]model.User, error) {
	return s.userRepo.List()
}

func (s *AuthService) DeleteUser(id int64) error {
	return s.userRepo.Delete(id)
}

func (s *AuthService) ChangePassword(userID int64, newPassword string) error {
	if len(newPassword) < 8 {
		return fmt.Errorf("password must be at least 8 characters")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return fmt.Errorf("hashing password: %w", err)
	}

	if err := s.userRepo.UpdatePassword(userID, string(hash)); err != nil {
		return fmt.Errorf("updating password: %w", err)
	}

	// Invalidate all sessions for this user
	return s.userRepo.DeleteUserSessions(userID)
}

func (s *AuthService) CleanExpiredSessions() error {
	return s.userRepo.CleanExpiredSessions()
}
