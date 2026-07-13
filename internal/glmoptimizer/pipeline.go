package glmoptimizer

// PreparedRequest is the immutable routing decision made at the API boundary.
// Later task-mode or fallback logic may propose a model, but SelectModel keeps
// GLM-5.2 packages inside their purchased model line.
type PreparedRequest struct {
	RequestedModel string
	LockedModel    string
	Body           []byte
	IsGLM52        bool
}

// PrepareRequest parses the request and applies the model lock before any
// smart-routing, task-mode, cache, or context transformation can run.
func PrepareRequest(body []byte, packageLabels ...string) (PreparedRequest, error) {
	req, err := ParseRequest(body)
	if err != nil {
		return PreparedRequest{}, err
	}

	prepared := PreparedRequest{
		RequestedModel: req.Model,
		LockedModel:    req.Model,
		Body:           append([]byte(nil), body...),
		IsGLM52:        IsGLM52Request(req.Model, packageLabels...),
	}
	if !prepared.IsGLM52 {
		return prepared, nil
	}

	prepared.Body, err = LockModel(body)
	if err != nil {
		return PreparedRequest{}, err
	}
	prepared.LockedModel = ModelGLM52
	return prepared, nil
}

// SelectModel enforces the model lock on every model proposal made by legacy
// smart-routing or task-mode code.
func (p PreparedRequest) SelectModel(candidate string) string {
	if p.IsGLM52 {
		return ModelGLM52
	}
	return candidate
}

// AllowsModel is used before fallback attempts. A GLM-5.2 request may only
// select another channel serving the same public model.
func (p PreparedRequest) AllowsModel(candidate string) bool {
	if !p.IsGLM52 {
		return true
	}
	return normalizeLabel(candidate) == ModelGLM52
}
