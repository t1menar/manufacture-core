package engine

// mergePatch applies an RFC 7396 JSON Merge Patch to target and returns the result.
func mergePatch(target, patch any) any {
	p, ok := patch.(map[string]any)
	if !ok {
		return patch
	}
	t, ok := target.(map[string]any)
	if !ok {
		t = map[string]any{}
	} else {
		c := make(map[string]any, len(t))
		for k, v := range t {
			c[k] = v
		}
		t = c
	}
	for k, v := range p {
		if v == nil {
			delete(t, k)
			continue
		}
		t[k] = mergePatch(t[k], v)
	}
	return t
}
