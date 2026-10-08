package auth_test

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"wf/backend/internal/platform/auth"
)

func TestSubjectFrom(t *testing.T) {
	user := &auth.Principal{UserID: uuid.New(), SessionID: uuid.New(), DeviceID: uuid.New()}
	staff := &auth.Staff{StaffID: uuid.New(), SessionID: uuid.New(), Role: "admin", Stage: auth.StageActive}
	bg := context.Background()
	cases := []struct {
		name string
		ctx  context.Context
		want auth.Subject
		ok   bool
	}{
		{"аноним", bg, auth.Subject{}, false},
		{"игрок", auth.With(bg, user), auth.Subject{Kind: auth.SubjectUser, ID: user.UserID, DeviceID: user.DeviceID}, true},
		{"сотрудник", auth.WithStaff(bg, staff), auth.Subject{Kind: auth.SubjectStaff, ID: staff.StaffID}, true},
		// сборка конвейера сломана — побеждает сотрудник, а не игрок
		{"оба", auth.WithStaff(auth.With(bg, user), staff), auth.Subject{Kind: auth.SubjectStaff, ID: staff.StaffID}, true},
		// нулевой id склеил бы ключи идемпотентности и лимиты разных людей
		{"игрок без id", auth.With(bg, &auth.Principal{}), auth.Subject{}, false},
		{"сотрудник без id", auth.WithStaff(bg, &auth.Staff{Role: "admin"}), auth.Subject{}, false},
	}
	for _, c := range cases {
		got, ok := auth.SubjectFrom(c.ctx)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("%s: %+v %v, нужно %+v %v", c.name, got, ok, c.want, c.ok)
		}
	}
}
