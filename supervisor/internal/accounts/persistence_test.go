package accounts

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFailedMutationKeepsPublishedAccounts(t *testing.T) {
	for _, op := range []string{"ensure", "create", "update", "delete", "access", "forget", "login"} {
		t.Run(op, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Load(dir)
			if err != nil {
				t.Fatal(err)
			}
			if op != "ensure" {
				if _, err := s.EnsureAdmin("admin", "password"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.Create("anna", "password", false, []string{"old"}); err != nil {
					t.Fatal(err)
				}
			}
			before := s.List()
			disk, _ := os.ReadFile(s.path)
			notices := 0
			s.OnChange = func(...string) { notices++ }
			p := filepath.Join(dir, "block")
			if err := os.WriteFile(p, nil, 0600); err != nil {
				t.Fatal(err)
			}
			s.path = filepath.Join(p, "accounts.json")
			err = nil
			switch op {
			case "ensure":
				_, err = s.EnsureAdmin("admin", "password")
			case "create":
				_, err = s.Create("bob", "password", false, nil)
			case "update":
				_, err = s.Update("anna", func(a *Account) { a.Admin = true; a.Desktops[0] = "changed" })
			case "delete":
				err = s.Delete("anna")
			case "access":
				err = s.SetAccess("new", []string{"anna"})
			case "forget":
				err = s.Forget(func(string) bool { return true })
			case "login":
				err = s.TouchLogin("anna")
			}
			if err == nil {
				t.Fatal("expected persistence error")
			}
			if !reflect.DeepEqual(s.List(), before) {
				t.Error("failed mutation published accounts")
			}
			if notices != 0 {
				t.Error("failed mutation notified")
			}
			got, _ := os.ReadFile(filepath.Join(dir, "accounts.json"))
			if string(got) != string(disk) {
				t.Error("disk changed")
			}
		})
	}
}
