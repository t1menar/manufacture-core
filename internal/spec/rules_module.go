package spec

import (
	"regexp"
	"strings"
)

// imageRef is a simplified OCI reference grammar (github.com/distribution/reference):
// [domain/]path[:tag][@digest]. As in Docker, the first component is a domain only
// when it is localhost or contains a dot or a port; otherwise it is part of the path.
var imageRef = func() *regexp.Regexp {
	const (
		label  = `(?:[a-zA-Z0-9]|[a-zA-Z0-9][a-zA-Z0-9-]*[a-zA-Z0-9])`
		domain = `(?:(?:localhost|` + label + `(?:\.` + label + `)+)(?::[0-9]+)?|` + label + `:[0-9]+)`
		comp   = `[a-z0-9]+(?:(?:[._]|__|-+)[a-z0-9]+)*`
		tag    = `(?P<tag>[\w][\w.-]{0,127})`
		digest = `(?P<digest>[A-Za-z][A-Za-z0-9]*(?:[-_+.][A-Za-z][A-Za-z0-9]*)*:[0-9a-fA-F]{32,})`
	)
	return regexp.MustCompile(`^(?:` + domain + `/)?` + comp + `(?:/` + comp + `)*(?::` + tag + `)?(?:@` + digest + `)?$`)
}()

// reservedEnvPrefix is used by the core and the SDK to configure modules.
const reservedEnvPrefix = "MF_"

func checkModule(r *reporter, m *Module) {
	spec := m.Spec
	sp := Path{"spec"}
	runtime := spec.EffectiveRuntime()

	switch {
	case spec.Image == "" && runtime == RuntimeAuto:
		r.errorf(sp, "image is required unless runtime is %q", RuntimeExternal)
	case spec.Image != "":
		checkImage(r, sp.Key("image"), spec.Image)
	}

	if rep := spec.Replicas; rep != nil && rep.Autoscale {
		rp := sp.Key("replicas")
		if rep.Min > rep.Max {
			r.errorf(rp, "min (%d) must not exceed max (%d)", rep.Min, rep.Max)
		}
		if runtime == RuntimeExternal {
			r.errorf(rp, "autoscaling needs runtime %q; an external module is scaled by whoever runs it", RuntimeAuto)
		}
	}

	envNames := map[string]bool{}
	for name := range spec.Env {
		envNames[name] = true
		if strings.HasPrefix(name, reservedEnvPrefix) {
			r.errorf(sp.Key("env").Key(name), "variables starting with %s are reserved for the core and the SDK", reservedEnvPrefix)
		}
	}
	seen := map[RunEvent]bool{}
	for i, sub := range spec.Subscribe {
		if seen[sub.Event] {
			r.errorf(sp.Key("subscribe").Index(i), "%s is already subscribed; list its pipelines in one entry", sub.Event)
		}
		seen[sub.Event] = true
	}
	for i, s := range spec.Secrets {
		p := sp.Key("secrets").Index(i).Key("env")
		switch {
		case strings.HasPrefix(s.Env, reservedEnvPrefix):
			r.errorf(p, "variables starting with %s are reserved for the core and the SDK", reservedEnvPrefix)
		case envNames[s.Env]:
			r.errorf(p, "variable %s is already set by env or another secret", s.Env)
		}
		envNames[s.Env] = true
	}
}

func checkImage(r *reporter, p Path, image string) {
	match := imageRef.FindStringSubmatch(image)
	if match == nil {
		r.errorf(p, "%q is not a valid image reference", image)
		return
	}
	tag, digest := match[imageRef.SubexpIndex("tag")], match[imageRef.SubexpIndex("digest")]
	if digest == "" && (tag == "" || tag == "latest") {
		r.warnf(p, "image %q is not pinned; use a version tag or an @sha256 digest so that rollouts are reproducible", image)
	}
}
