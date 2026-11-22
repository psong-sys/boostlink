package main

import (
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
