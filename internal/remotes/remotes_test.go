package remotes

import "testing"

func TestValidate(t *testing.T) {
	good := []Remote{{ID: "vm-1", Name: "VM 1", Host: "vm1.tailnet.ts.net", User: "daniel", Port: 22, Mode: "tailscale"}}
	if err := Validate(good); err != nil {
		t.Fatalf("validate good: %v", err)
	}
	bin, args := ExecArgs(&good[0], "pwd")
	if bin != "tailscale" || len(args) == 0 {
		t.Fatalf("tailscale exec args: %s %v", bin, args)
	}
	sshRemote := Remote{ID: "a", Name: "n", Host: "10.0.0.2", User: "root", Port: 2222, Mode: "ssh"}
	bin, args = ExecArgs(&sshRemote, "pwd")
	if bin != "ssh" || len(args) < 6 {
		t.Fatalf("ssh exec args: %s %v", bin, args)
	}
	bin, args = ShellArgs(&sshRemote, "", "")
	if bin != "ssh" || args[0] != "-tt" {
		t.Fatalf("ssh shell args: %s %v", bin, args)
	}
	bad := []Remote{{ID: "BAD ID!", Name: "", Host: "", User: "", Port: 0, Mode: "x"}}
	if err := Validate(bad); err == nil {
		t.Fatalf("expected validation error")
	}
	dup := []Remote{good[0], good[0]}
	if err := Validate(dup); err == nil {
		t.Fatalf("expected duplicate error")
	}
	if Lookup(good, "vm-1") == nil || Lookup(good, "nope") != nil {
		t.Fatalf("lookup failed")
	}
}
