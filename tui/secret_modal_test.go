package tui

import (
	"strings"
	"testing"
)

func TestPrivateSourceModalNeverDisplaysEnteredURL(t *testing.T) {
	secret := "https://provider.invalid/profile?token=private"
	resource := NewModel().Apply(eventFromJSON(t, `{"Snapshot":{"Resources":[{"ID":"geo","Kind":"rule-set","Format":"yaml","RuleType":"domain","Enabled":true}]}}`))
	resource.Tab = TabResources
	resource.Selection[TabResources] = "resource:geo"
	resource, _, _ = resource.HandleKey("e")
	dns := dnsPolicyModel(t)
	dns.Selection[TabSettings] = "dns:set:one"
	dns, _, _ = dns.HandleKey("g")
	for _, item := range []struct {
		model Model
		field string
	}{{resource, "url"}, {dns, "endpoints"}} {
		model := item.model
		if model.Modal == nil || model.Modal.Form == nil {
			t.Fatal("private source form missing")
		}
		model = moveToFormField(t, model, item.field)
		model, _, _ = model.HandleKey("enter")
		model = typeFormText(t, model, secret)
		text := strings.Join(mockRender(t, model, 80, 20), "\n")
		if strings.Contains(text, "provider.invalid") || strings.Contains(text, "token=private") {
			t.Fatalf("private source rendered in form: %q", text)
		}
	}
	binary := NewModel().selectTab(TabSettings)
	binary, _, _ = binary.HandleKey("b")
	if binary.Modal == nil || binary.Modal.Form != nil {
		t.Fatal("binary selector must remain simple")
	}
}
