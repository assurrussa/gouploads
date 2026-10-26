//nolint:stylecheck // package name is intentional
package shared

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

type (
	UserType         string
	FileObjectType   string
	FileObjectID     int64
	ServicePrincipal struct {
		SiteID   uuid.UUID `json:"siteId"`
		ClientID string    `json:"clientId"`
	}
	FileUploader struct {
		Service   *ServicePrincipal    `json:"service,omitempty"`
		UserID    int64                `json:"userId"`
		UserUUID  sharedtypes.UserID   `json:"userUuid"`
		Type      UserType             `json:"type"`
		Status    FileUploadTaskStatus `json:"status"`
		AfterJobs []FileEventAfterJob  `json:"uploadAfterJobs"`
	}
)

const (
	UserTypeService UserType = "service"
	UserTypeAdmin   UserType = "admin"
	UserTypeUser    UserType = "user"
)

func (o *FileObjectID) Int64() int64 {
	if o == nil {
		return 0
	}

	return int64(*o)
}

func (o *FileObjectID) String() string {
	if o == nil {
		return ""
	}

	return strconv.FormatInt(int64(*o), 10)
}

const (
	ObjectTypeAdmin         FileObjectType = "admin"
	ObjectTypeBonusLesson   FileObjectType = "bonus_lesson"
	ObjectTypeKnowledgeBase FileObjectType = "knowledge_base"
	ObjectTypeExercise      FileObjectType = "exercise"
)

func (o FileObjectType) Validate() error {
	const maxObjectTypeLength = 64

	value := string(o)
	if len(value) == 0 || len(value) > maxObjectTypeLength || value[0] < 'a' || value[0] > 'z' {
		return fmt.Errorf("invalid object type: %s", o)
	}

	for i := 1; i < len(value); i++ {
		char := value[i]
		if char >= 'a' && char <= 'z' || char >= '0' && char <= '9' || char == '_' {
			continue
		}

		return fmt.Errorf("invalid object type: %s", o)
	}

	return nil
}

func (o FileObjectType) String() string {
	return string(o)
}

// Validate checks provenance, not authorization. Hosts must authorize the site and object.
func (p ServicePrincipal) Validate() error {
	if p.SiteID == uuid.Nil || !utf8.ValidString(p.ClientID) || strings.TrimSpace(p.ClientID) != p.ClientID || p.ClientID == "" ||
		len(p.ClientID) > 255 || strings.IndexFunc(p.ClientID, unicode.IsControl) >= 0 {
		return errors.New("invalid service principal")
	}
	return nil
}

func (o FileUploader) Validate() error {
	if o.Type == UserTypeService {
		if o.Service == nil || o.UserID != 0 || !o.UserUUID.IsZero() {
			return errors.New("invalid service uploader")
		}
		return o.Service.Validate()
	}
	if o.Service != nil {
		return errors.New("human uploader cannot have service provenance")
	}

	if o.UserUUID == sharedtypes.UserIDNil {
		return errors.New("invalid object UserUUID")
	}

	if o.UserID == 0 {
		return errors.New("invalid object UserID")
	}

	switch o.Type {
	case UserTypeAdmin, UserTypeUser:
		return nil
	default:
		return errors.New("invalid object type")
	}
}
