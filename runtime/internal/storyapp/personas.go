package storyapp

import (
	"context"
	"database/sql"
	"errors"
	"strings"
)

// Personas are reusable lead-character templates owned by one account. A template
// is copied into a world at creation time; later edits never touch existing worlds.
type Persona struct {
	PersonaID string `json:"persona_id"`
	Name      string `json:"name"`
	Profile   string `json:"profile"`
	Version   int64  `json:"version"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

type PersonaRequest struct {
	Name            string `json:"name"`
	Profile         string `json:"profile"`
	ExpectedVersion int64  `json:"expected_version"`
}

func (a *App) ListPersonas(ctx context.Context) ([]Persona, error) {
	rows, err := a.appDB.QueryContext(ctx, `SELECT persona_id,name,profile,version,created_at,updated_at FROM personas WHERE user_id=? ORDER BY updated_at DESC, persona_id`, a.userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Persona{}
	for rows.Next() {
		var p Persona
		if err = rows.Scan(&p.PersonaID, &p.Name, &p.Profile, &p.Version, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (a *App) CreatePersona(ctx context.Context, request PersonaRequest) (Persona, error) {
	name, profile, err := validatePersona(request)
	if err != nil {
		return Persona{}, err
	}
	persona := Persona{PersonaID: newID("persona"), Name: name, Profile: profile, Version: 1, CreatedAt: nowText(), UpdatedAt: nowText()}
	if _, err = a.appDB.ExecContext(ctx, `INSERT INTO personas(user_id,persona_id,name,profile,version,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`,
		a.userID, persona.PersonaID, persona.Name, persona.Profile, persona.Version, persona.CreatedAt, persona.UpdatedAt); err != nil {
		return Persona{}, err
	}
	return persona, nil
}

func (a *App) ReadPersona(ctx context.Context, personaID string) (Persona, error) {
	var p Persona
	err := a.appDB.QueryRowContext(ctx, `SELECT persona_id,name,profile,version,created_at,updated_at FROM personas WHERE user_id=? AND persona_id=?`, a.userID, strings.TrimSpace(personaID)).
		Scan(&p.PersonaID, &p.Name, &p.Profile, &p.Version, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Persona{}, ErrPersonaNotFound
	}
	if err != nil {
		return Persona{}, err
	}
	return p, nil
}

func (a *App) UpdatePersona(ctx context.Context, personaID string, request PersonaRequest) (Persona, error) {
	name, profile, err := validatePersona(request)
	if err != nil {
		return Persona{}, err
	}
	if request.ExpectedVersion < 1 {
		return Persona{}, ErrInvalidRequest
	}
	result, err := a.appDB.ExecContext(ctx, `UPDATE personas SET name=?,profile=?,version=version+1,updated_at=? WHERE user_id=? AND persona_id=? AND version=?`,
		name, profile, nowText(), a.userID, strings.TrimSpace(personaID), request.ExpectedVersion)
	if err != nil {
		return Persona{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Persona{}, err
	}
	if affected == 0 {
		// Either the template is gone or another edit won the version race.
		if _, readErr := a.ReadPersona(ctx, personaID); errors.Is(readErr, ErrPersonaNotFound) {
			return Persona{}, ErrPersonaNotFound
		} else if readErr != nil {
			return Persona{}, readErr
		}
		return Persona{}, ErrVersionConflict
	}
	return a.ReadPersona(ctx, personaID)
}

func (a *App) DeletePersona(ctx context.Context, personaID string) error {
	result, err := a.appDB.ExecContext(ctx, `DELETE FROM personas WHERE user_id=? AND persona_id=?`, a.userID, strings.TrimSpace(personaID))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrPersonaNotFound
	}
	// Worlds already created from this template keep their copied name and profile.
	return nil
}

func validatePersona(request PersonaRequest) (string, string, error) {
	name := cleanText(request.Name)
	profile := cleanText(request.Profile)
	if name == "" || len([]rune(name)) > 80 || len([]rune(profile)) > 2000 {
		return "", "", ErrInvalidRequest
	}
	return name, profile, nil
}

// personaDefaults resolves the template selected for a new world. The pack's own
// defaults stay in use when no template is chosen.
func (a *App) personaDefaults(ctx context.Context, personaID string, def gameDefinition) (string, string, error) {
	if strings.TrimSpace(personaID) == "" {
		return "", "", nil
	}
	persona, err := a.ReadPersona(ctx, personaID)
	if err != nil {
		return "", "", err
	}
	if !def.Summary.Player.Editable {
		return "", "", ErrInvalidRequest
	}
	return persona.Name, persona.Profile, nil
}
