package roost

import (
	"strings"
	"testing"
)

// A hosted framework service sometimes has an OPTIONAL collaborator that is
// not a NewMod parameter — platform's pending-order index is the first
// (kit `Mod.WithPendingOrders`). The generated wiring has to call it, or the
// project's collaborators file can define it and nothing will ever read it.
//
// The second promise here is the configuration block: platform's Mod refuses
// at Init when `session_secret` or `payment_secret` is empty, so a generated
// starter config that omits them produces a process that cannot start. The
// account block already emits `session_secret: CHANGE_ME` for exactly this
// reason.

func TestHostedServiceChainsItsOptionalCollaborators(t *testing.T) {
	generated := renderBootstrap(gameTemplateManifest(t))
	const want = "svcplatform.NewMod(servicePlatform.Verify(), servicePlatform.Players(), servicePlatform.Deliver(), servicePlatform.Metrics()).WithPendingOrders(servicePlatform.Pending())"
	if !strings.Contains(generated, want) {
		t.Errorf("the platform Mod is wired without its pending-order index:\n%s", generated)
	}
}

func TestPlatformConfigCarriesTheSecretsItsModRequires(t *testing.T) {
	block := frameworkCatalog["platform"].ConfigFunc("demo")
	for _, key := range []string{"session_secret:", "payment_secret:"} {
		if !strings.Contains(block, key) {
			t.Errorf("platform config block omits %s, so the process refuses to start:\n%s", key, block)
		}
	}
}

// The default collaborators file has to define everything the wiring names,
// including the optional ones, or the project does not compile.
func TestDefaultCollaboratorsDefineEveryWiredName(t *testing.T) {
	body := renderFrameworkCollaborators(gameTemplateManifest(t), "platform")
	for _, fn := range []string{"func Verify()", "func Players()", "func Deliver()", "func Pending()", "func Metrics()"} {
		if !strings.Contains(body, fn) {
			t.Errorf("collaborators file is missing %s:\n%s", fn, body)
		}
	}
}
