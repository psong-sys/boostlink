package main

// The card editor talks to Server-Infinite's own web API (the one its Blazor
// card page uses) on 127.0.0.1:28080. Every read is
// GET /ui/<area>/<get>/<accessCode>/<chipId>, and every save is a POST with
// {accessCode, chipId, <field>: <the same object>}. The launcher passes the
// objects through untouched, so fields it doesn't know about are kept.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type cardSection struct {
	get, post, field string
}

var cardSections = map[string]cardSection{
	"profile": {"/ui/card/getBasicDisplayProfile/", "/ui/card/updateBasicProfile", "basicProfile"},
	"suits":   {"/ui/mobileSuit/getAllFavouriteMs/", "/ui/mobileSuit/updateAllFavouriteMs", "favouriteMsList"},
	"comms":   {"/ui/message/getCustomMessageGroupSetting/", "/ui/message/upsertCustomMessages", "messageSetting"},
	"sticker": {"/ui/sticker/getDefaultSticker/", "/ui/sticker/updateDefaultSticker", "stickerDto"},
	"buttons": {"/ui/gamepad/getGamepadConfig/", "/ui/gamepad/upsertGamepadConfig", "gamepadConfig"},
	"navi":    {"/ui/navi/getNaviProfile/", "", ""},
}

// The catalogs the editor shows, read from the card server's own data folder.
var cardCatalogs = map[string]string{
	"suits":     "MobileSuits.json",
	"bgms":      "Bgms.json",
	"gauges":    "Gauges.json",
	"buttons":   "GamepadOptions.json",
	"trackers":  "sticker/TrackerTypes.json",
	"stbg":      "sticker/Backgrounds.json",
	"steffect":  "sticker/Effects.json",
	"comments":  "comment/CustomizeComment.json",
	"phrases":   "comment/CustomizeCommentPhrase.json",
	"titles":    "titles/Titles.json",
	"templates": "messages/MessageTemplates.json",
	"navis":     "Navigators.json",
}

var cardHTTP = &http.Client{Timeout: 15 * time.Second}

func cardBase() string { return fmt.Sprintf("http://127.0.0.1:%d", cardPort) }

func serverDir() string {
	return filepath.Dir(resolvePaths(loadLauncherConfig()).ServerExe)
}

// cardIDs reads the card the game uses from card.ini.
func cardIDs() (ac, chip string, err error) {
	s, _, err := readSettings()
	if err != nil {
		return "", "", err
	}
	ac, chip = strings.TrimSpace(s.Card.AccessCode), strings.TrimSpace(s.Card.ChipID)
	if ac == "" || chip == "" {
		return "", "", errString("card.ini has no chip ID or access code yet. Set them on the PILOT tab first")
	}
	return ac, chip, nil
}

func cardGet(sec cardSection, ac, chip string) (json.RawMessage, error) {
	u := cardBase() + sec.get + url.PathEscape(ac) + "/" + url.PathEscape(chip)
	resp, err := cardHTTP.Get(u)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("card server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	b = bytes.TrimSpace(b)
	if len(b) == 0 || string(b) == "null" {
		return nil, errString("this card isn't on the card server yet")
	}
	if !json.Valid(b) {
		return nil, errString("the card server sent something that isn't JSON")
	}
	return b, nil
}

type cardLoadResult struct {
	ChipID     string                     `json:"chipId"`
	AccessCode string                     `json:"accessCode"`
	Sections   map[string]json.RawMessage `json:"sections"`
	Errors     map[string]string          `json:"errors"`
}

func cardLoad() (*cardLoadResult, error) {
	ac, chip, err := cardIDs()
	if err != nil {
		return nil, err
	}
	if err := runner.StartCardServer(); err != nil {
		return nil, fmt.Errorf("couldn't start the card server: %v", err)
	}
	res := &cardLoadResult{ChipID: chip, AccessCode: ac, Sections: map[string]json.RawMessage{}, Errors: map[string]string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for name, sec := range cardSections {
		wg.Add(1)
		go func(name string, sec cardSection) {
			defer wg.Done()
			v, err := cardGet(sec, ac, chip)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				res.Errors[name] = err.Error()
			} else {
				res.Sections[name] = v
			}
		}(name, sec)
	}
	wg.Wait()
	if _, ok := res.Sections["profile"]; !ok {
		return nil, errString("Your card isn't on the card server yet. Press PLAY and insert your card in the game once, then come back here. (" + res.Errors["profile"] + ")")
	}
	return res, nil
}

var (
	cardDBMu   sync.Mutex
	cardDBDone bool
)

// backupCard copies the card server's database once per launcher run, and
// saves the section's current values before every change.
func backupCard(section string, current json.RawMessage) error {
	dir := filepath.Join(resolvePaths(loadLauncherConfig()).BackupDir, "card")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405")
	var dbErr error
	cardDBMu.Lock()
	if !cardDBDone {
		dst := filepath.Join(dir, "db-"+stamp)
		dbErr = os.MkdirAll(dst, 0755)
		for _, n := range []string{"ServerInfinite.db", "ServerInfinite.db-wal", "ServerInfinite.db-shm"} {
			if dbErr != nil {
				break
			}
			b, err := os.ReadFile(filepath.Join(serverDir(), n))
			if err != nil {
				if n == "ServerInfinite.db" {
					dbErr = err
				}
				continue
			}
			dbErr = os.WriteFile(filepath.Join(dst, n), b, 0644)
		}
		cardDBDone = dbErr == nil
	}
	cardDBMu.Unlock()
	if dbErr != nil {
		return fmt.Errorf("couldn't back up the card database: %v", dbErr)
	}
	return os.WriteFile(filepath.Join(dir, section+"-"+stamp+".json"), current, 0644)
}

func cardSave(section string, value json.RawMessage) error {
	sec, ok := cardSections[section]
	if !ok || sec.post == "" {
		return errString("unknown card section " + section)
	}
	if !json.Valid(value) {
		return errString("bad data")
	}
	if runner.Busy() {
		return errString("close the game before changing your card (the game saves the card when you leave)")
	}
	ac, chip, err := cardIDs()
	if err != nil {
		return err
	}
	if err := runner.StartCardServer(); err != nil {
		return err
	}
	cur, err := cardGet(sec, ac, chip)
	if err != nil {
		return err
	}
	if err := backupCard(section, cur); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"accessCode": ac, "chipId": chip, sec.field: value})
	resp, err := cardHTTP.Post(cardBase()+sec.post, "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return fmt.Errorf("card server answered %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var r map[string]any
	if json.Unmarshal(b, &r) == nil {
		for k, v := range r {
			if strings.EqualFold(k, "success") && v == false {
				return errString("the card server didn't accept the change")
			}
		}
	}
	return nil
}

func cardCatalog(name string) ([]byte, error) {
	rel, ok := cardCatalogs[name]
	if !ok {
		return nil, errString("unknown list")
	}
	b, err := os.ReadFile(filepath.Join(serverDir(), "wwwroot", "data", filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	return bytes.TrimPrefix(b, []byte("\xEF\xBB\xBF")), nil
}

func (u *uiServer) cardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/card/load", func(w http.ResponseWriter, r *http.Request) {
		u.touch()
		res, err := cardLoad()
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, res)
	})
	mux.HandleFunc("POST /api/card/save", func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			http.Error(w, "forbidden", 403)
			return
		}
		b, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if err != nil {
			writeErr(w, err)
			return
		}
		if err := cardSave(r.URL.Query().Get("section"), b); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/card/catalog/{name}", func(w http.ResponseWriter, r *http.Request) {
		b, err := cardCatalog(r.PathValue("name"))
		if err != nil {
			writeErr(w, err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "max-age=3600")
		_, _ = w.Write(b)
	})
	// Pictures (gauges, sticker backgrounds and so on) from the card server's folder.
	mux.HandleFunc("GET /cardimg/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "max-age=3600")
		http.StripPrefix("/cardimg/", http.FileServer(http.Dir(cardImageDir()))).ServeHTTP(w, r)
	})
}

// cardImageDir is the card server's picture folder: Server\images on the old
// build, Server\wwwroot\images on the new one.
func cardImageDir() string { return cardImageDirIn(serverDir()) }

func cardImageDirIn(dir string) string {
	for _, d := range []string{filepath.Join(dir, "images"), filepath.Join(dir, "wwwroot", "images")} {
		if st, err := os.Stat(filepath.Join(d, "mobile_suits")); err == nil && st.IsDir() {
			return d
		}
	}
	return filepath.Join(dir, "images")
}
