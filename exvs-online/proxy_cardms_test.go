package main

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"exvsonline/pb/exvs"

	"google.golang.org/protobuf/proto"
)

func emptyPreLoad() *exvs.Response_PreLoadCard {
	return &exvs.Response_PreLoadCard{User: &exvs.Response_PreLoadCard_MobileUserGroup{}}
}

func TestPatchCardMobileSuit(t *testing.T) {
	var path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		w.Write([]byte(`[{"msId":135,"burstType":"Fighting"},{"msId":2}]`))
	}))
	defer srv.Close()
	up, _ := url.Parse(srv.URL)
	p := &Proxy{upstream: up}
	rq := &exvs.Request_PreLoadCard{AccessCode: proto.String("123"), ChipId: proto.String("abc")}

	pl := emptyPreLoad()
	if !p.patchCardMobileSuit(rq, pl) || pl.User.FavoriteMobileSuits[0].GetMstMobileSuitId() != 135 {
		t.Fatalf("first favorite not used: %v", pl)
	}
	if path != "/ui/mobileSuit/getAllFavouriteMs/123/abc" {
		t.Errorf("asked %s", path)
	}
	if p.patchCardMobileSuit(rq, pl) {
		t.Error("replaced favorites the card server already sent")
	}
	p.cfg.CardMobileSuit = 1
	pl = emptyPreLoad()
	if !p.patchCardMobileSuit(rq, pl) || pl.User.FavoriteMobileSuits[0].GetMstMobileSuitId() != 1 {
		t.Fatalf("cardMobileSuit not used: %v", pl)
	}
	if p.patchCardMobileSuit(rq, nil) || p.patchCardMobileSuit(rq, &exvs.Response_PreLoadCard{}) {
		t.Error("patched a reply without a user")
	}
}

func TestFirstSuitID(t *testing.T) {
	for in, want := range map[string]uint32{
		`[{"msId":7}]`: 7, `{"favouriteMsList":[{"MsId":9}]}`: 9, `[]`: 0, `null`: 0, `x`: 0, `[{"msId":0}]`: 0,
	} {
		if got := firstSuitID([]byte(in)); got != want {
			t.Errorf("%s: got %d want %d", in, got, want)
		}
	}
}

// The new build sends RegisterCard with an empty session_id; the proxy must put
// back the one the card server gave that card in PreLoadCard.
func TestRegisterCardSessionFilled(t *testing.T) {
	var got *exvs.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		req := &exvs.Request{}
		if err := (proto.UnmarshalOptions{AllowPartial: true}).Unmarshal(b, req); err != nil {
			t.Fatal(err)
		}
		resp := &exvs.Response{Type: req.Type, RequestId: req.RequestId, Error: exvs.Error_SUCCESS.Enum()}
		if req.GetType() == exvs.MethodType_MTHD_PRE_LOAD_CARD {
			resp.PreLoadCard = &exvs.Response_PreLoadCard{SessionId: proto.String("0741151795609085"), IsNewCard: proto.Bool(true)}
		}
		got = req
		out, _ := proto.MarshalOptions{AllowPartial: true}.Marshal(resp)
		w.Write(out)
	}))
	defer srv.Close()
	up, _ := url.Parse(srv.URL)
	p := &Proxy{upstream: up, client: srv.Client(), tl: &trafficLog{}}
	send := func(req *exvs.Request) {
		b, _ := proto.MarshalOptions{AllowPartial: true}.Marshal(req)
		rr := httptest.NewRecorder()
		p.ServeHTTP(rr, httptest.NewRequest("POST", "/", bytes.NewReader(b)))
		if rr.Code != 200 {
			t.Fatal(rr.Code)
		}
	}
	card := func() (*string, *string) { return proto.String("074115"), proto.String("exvs2ib931") }
	ac, chip := card()
	send(&exvs.Request{Type: exvs.MethodType_MTHD_PRE_LOAD_CARD.Enum(), RequestId: proto.String("a"),
		PreLoadCard: &exvs.Request_PreLoadCard{IsCard: proto.Bool(true), AccessCode: ac, ChipId: chip, MuchaCountryCode: proto.String("JPN")}})
	ac, chip = card()
	send(&exvs.Request{Type: exvs.MethodType_MTHD_REGISTER_CARD.Enum(), RequestId: proto.String("b"),
		RegisterCard: &exvs.Request_RegisterCard{SessionId: proto.String(""), IsCard: proto.Bool(true), AccessCode: ac, ChipId: chip, MuchaCountryCode: proto.String("JPN")}})
	if got.GetRegisterCard().GetSessionId() != "0741151795609085" {
		t.Fatalf("session not filled: %v", got)
	}
	// Another card's RegisterCard is left alone.
	send(&exvs.Request{Type: exvs.MethodType_MTHD_REGISTER_CARD.Enum(), RequestId: proto.String("c"),
		RegisterCard: &exvs.Request_RegisterCard{SessionId: proto.String(""), IsCard: proto.Bool(true), AccessCode: proto.String("1"), ChipId: proto.String("x"), MuchaCountryCode: proto.String("JPN")}})
	if got.GetRegisterCard().GetSessionId() != "" {
		t.Fatalf("filled the wrong card: %v", got)
	}
}
