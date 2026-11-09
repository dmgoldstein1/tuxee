package server

import (
	"encoding/json"
	"net/http"
	"tuxee/internal/files"
	"tuxee/internal/i18n"
	"tuxee/internal/remotes"
)

func (s *Server) remotesOperation(w http.ResponseWriter, r *http.Request, session *login) {
	switch r.Method {
	case "GET":
		s.settingsMu.Lock()
		defer s.settingsMu.Unlock()
		settings, err := LoadSettings(r.Context(), session.user)
		if err != nil {
			failure(w, 500, err)
			return
		}
		if settings.Remotes == nil {
			settings.Remotes = []remotes.Remote{}
		}
		jsonResponse(w, 200, settings.Remotes)
	case "PUT":
		var input []remotes.Remote
		if err := decode(w, r, &input); err != nil {
			failure(w, 400, err)
			return
		}
		if input == nil {
			input = []remotes.Remote{}
		}
		if err := remotes.Validate(input); err != nil {
			failure(w, 400, err)
			return
		}
		s.settingsMu.Lock()
		defer s.settingsMu.Unlock()
		settings, err := LoadSettings(r.Context(), session.user)
		if err != nil {
			failure(w, 500, err)
			return
		}
		settings.Remotes = input
		if err := validateSettings(&settings); err != nil {
			failure(w, 400, err)
			return
		}
		data, _ := json.Marshal(settings)
		if _, err := files.Run(r.Context(), session.user, files.Request{Operation: "settings-save", Settings: data}); err != nil {
			failure(w, 400, err)
			return
		}
		background, foreground := settings.terminalColors()
		s.pruneEditors(r.Context(), session.user, settings)
		s.Terminals.SetBackground(session.user, background, foreground)
		jsonResponse(w, 200, settings.Remotes)
	default:
		failure(w, 405, i18n.New("backend.server.methodNotAllowed"))
	}
}
