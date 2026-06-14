package images

import (
	"encoding/json"
	"testing"
)

func TestCatalogModelLookup(t *testing.T) {
	cap, ok := GetModelCapability(ProviderAgnes, "agnes-image-2.1-flash")
	if !ok {
		t.Fatal("expected agnes-image-2.1-flash to exist")
	}
	if cap.Provider != ProviderAgnes {
		t.Errorf("provider mismatch: got %s, want %s", cap.Provider, ProviderAgnes)
	}
	if cap.MediaType != MediaTypeImage {
		t.Errorf("media type mismatch: got %s, want %s", cap.MediaType, MediaTypeImage)
	}
	if cap.Deprecated {
		t.Error("2.1 should not be deprecated")
	}
	if cap.MaxReferences != 1 {
		t.Errorf("max references mismatch: got %d, want 1", cap.MaxReferences)
	}

	dep, ok := GetModelCapability(ProviderAgnes, "agnes-image-1.2")
	if !ok {
		t.Fatal("expected agnes-image-1.2 to exist")
	}
	if !dep.Deprecated {
		t.Error("1.2 should be deprecated")
	}

	if _, ok := GetModelCapability(ProviderAgnes, "does-not-exist"); ok {
		t.Error("should not find non-existent model")
	}
}

func TestCatalogDefaultModels(t *testing.T) {
	def := DefaultModel(ProviderXAI, MediaTypeImage)
	if def == "" {
		t.Error("xAI default image model should not be empty")
	}
	def = DefaultModel(ProviderAgnes, MediaTypeImage)
	if def != "agnes-image-2.1-flash" {
		t.Errorf("agnes default image model: got %s, want agnes-image-2.1-flash", def)
	}
	def = DefaultModel(ProviderAgnes, MediaTypeVideo)
	if def != "agnes-video-v2.0" {
		t.Errorf("agnes default video model: got %s, want agnes-video-v2.0", def)
	}
}

func TestCatalogList(t *testing.T) {
	all := ListModelCapabilities(true)
	foundDeprecated := false
	for _, m := range all {
		if m.Deprecated {
			foundDeprecated = true
			break
		}
	}
	if !foundDeprecated {
		t.Error("includeDeprecated=true should include deprecated models")
	}
	nonDep := ListModelCapabilities(false)
	for _, m := range nonDep {
		if m.Deprecated {
			t.Errorf("non-deprecated list included deprecated model %s", m.Model)
		}
	}
	if len(nonDep) >= len(all) {
		t.Error("non-deprecated list should have fewer models")
	}
	agnesImages := ModelsForProvider(ProviderAgnes, MediaTypeImage, false)
	for _, m := range agnesImages {
		if m.Provider != ProviderAgnes || m.MediaType != MediaTypeImage {
			t.Errorf("filter wrong: provider=%s media=%s", m.Provider, m.MediaType)
		}
		if m.Deprecated {
			t.Errorf("deprecated model in non-deprecated list: %s", m.Model)
		}
	}
}

func TestValidateNumFrames(t *testing.T) {
	cases := []struct {
		n       int
		max     int
		wantErr bool
	}{
		{0, 100, true},
		{-1, 100, true},
		{1, 100, false},
		{9, 100, false},
		{17, 100, false},
		{81, 100, false},
		{121, 1000, false},
		{241, 1000, false},
		{441, 441, false},
		{449, 441, true},
		{82, 100, true},
		{2, 100, true},
		{500, 441, true},
	}
	for i, c := range cases {
		err := ValidateNumFrames(c.n, c.max)
		gotErr := err != nil
		if gotErr != c.wantErr {
			t.Errorf("case %d: n=%d max=%d: got err=%v wantErr=%v", i, c.n, c.max, err, c.wantErr)
		}
	}
}

func TestValidateFrameRate(t *testing.T) {
	cases := []struct {
		fr      int
		min     int
		max     int
		wantErr bool
	}{
		{24, 1, 60, false},
		{1, 1, 60, false},
		{60, 1, 60, false},
		{0, 1, 60, true},
		{61, 1, 60, true},
		{-1, 1, 60, true},
		{30, 15, 30, false},
		{14, 15, 30, true},
	}
	for i, c := range cases {
		err := ValidateFrameRate(c.fr, c.min, c.max)
		gotErr := err != nil
		if gotErr != c.wantErr {
			t.Errorf("case %d: fr=%d min=%d max=%d: got err=%v wantErr=%v", i, c.fr, c.min, c.max, err, c.wantErr)
		}
	}
}

func TestParseSize(t *testing.T) {
	cases := []struct {
		input    string
		wantW    int
		wantH    int
		wantErr  bool
	}{
		{"1024x768", 1024, 768, false},
		{"768x1024", 768, 1024, false},
		{"1152x768", 1152, 768, false},
		{"1x1", 1, 1, false},
		{"", 0, 0, true},
		{"1024", 0, 0, true},
		{"abcxdef", 0, 0, true},
		{"1024X768", 0, 0, true},
		{"0x768", 0, 0, true},
		{"-10x20", 0, 0, true},
	}
	for i, c := range cases {
		w, h, err := ParseSize(c.input)
		gotErr := err != nil
		if gotErr != c.wantErr {
			t.Errorf("case %d: input=%q: got err=%v wantErr=%v", i, c.input, err, c.wantErr)
			continue
		}
		if !c.wantErr && (w != c.wantW || h != c.wantH) {
			t.Errorf("case %d: input=%q: got %dx%d, want %dx%d", i, c.input, w, h, c.wantW, c.wantH)
		}
	}
}

func TestValidateProviderAndMediaType(t *testing.T) {
	if err := ValidateProvider(ProviderXAI); err != nil {
		t.Errorf("xai should be valid, got %v", err)
	}
	if err := ValidateProvider(ProviderAgnes); err != nil {
		t.Errorf("agnes should be valid, got %v", err)
	}
	if err := ValidateProvider("invalid"); err == nil {
		t.Error("invalid provider should fail")
	}
	if err := ValidateMediaType(MediaTypeImage); err != nil {
		t.Errorf("image should be valid, got %v", err)
	}
	if err := ValidateMediaType(MediaTypeVideo); err != nil {
		t.Errorf("video should be valid, got %v", err)
	}
	if err := ValidateMediaType("audio"); err == nil {
		t.Error("audio should fail")
	}
	if NormalizeProvider("  AGNES ") != ProviderAgnes {
		t.Error("provider normalization failed")
	}
	if NormalizeMediaType("  VIDEO ") != MediaTypeVideo {
		t.Error("media type normalization failed")
	}
}

func TestAgnesImagePayloadTextToImage(t *testing.T) {
	req := ImagineRequest{
		Provider:       ProviderAgnes,
		Mode:           ModeTextToImage,
		Model:          "agnes-image-2.1-flash",
		Prompt:         "a serene mountain landscape at dawn",
		Size:           "1024x768",
		ResponseFormat: "url",
		N:              1,
	}
	endpoint, payload, err := agnesImagePayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint != agnesImagesEndpoint {
		t.Errorf("endpoint: got %s, want %s", endpoint, agnesImagesEndpoint)
	}
	if payload["model"] != "agnes-image-2.1-flash" {
		t.Errorf("model: got %v", payload["model"])
	}
	if payload["prompt"] != req.Prompt {
		t.Errorf("prompt mismatch")
	}
	if payload["size"] != "1024x768" {
		t.Errorf("size: got %v", payload["size"])
	}
	extra, ok := payload["extra_body"].(map[string]any)
	if !ok {
		t.Fatal("expected extra_body in payload")
	}
	if extra["response_format"] != "url" {
		t.Errorf("response_format in extra_body: got %v", extra["response_format"])
	}
	if _, hasImg := extra["image"]; hasImg {
		t.Error("text_to_image should not have image in extra_body")
	}
}

func TestAgnesImagePayloadImageToImage(t *testing.T) {
	req := ImagineRequest{
		Provider: ProviderAgnes,
		Mode:     ModeImageToImage,
		Model:    "agnes-image-2.1-flash",
		Prompt:   "change background to sunset",
		Size:     "1024x768",
		Images: []ImageInput{
			{URL: "https://example.com/input.png", SourceType: "url"},
		},
	}
	_, payload, err := agnesImagePayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	extra, ok := payload["extra_body"].(map[string]any)
	if !ok {
		t.Fatal("expected extra_body")
	}
	imgs, ok := extra["image"].([]string)
	if !ok || len(imgs) != 1 {
		t.Fatalf("expected 1 image, got %#v", extra["image"])
	}
	if imgs[0] != "https://example.com/input.png" {
		t.Errorf("image url: got %s", imgs[0])
	}
}

func TestAgnesImagePayloadMultiImage(t *testing.T) {
	req := ImagineRequest{
		Provider: ProviderAgnes,
		Mode:     ModeMultiImageEdit,
		Model:    "agnes-image-2.0-flash",
		Prompt:   "merge these styles",
		Size:     "1024x768",
		Images: []ImageInput{
			{URL: "https://example.com/a.png"},
			{URL: "https://example.com/b.png"},
			{URL: "https://example.com/c.png"},
		},
	}
	_, payload, err := agnesImagePayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	extra, _ := payload["extra_body"].(map[string]any)
	imgs, ok := extra["image"].([]string)
	if !ok || len(imgs) != 3 {
		t.Fatalf("expected 3 images, got %#v", extra["image"])
	}
}

func TestAgnesImagePayloadB64(t *testing.T) {
	req := ImagineRequest{
		Provider:       ProviderAgnes,
		Mode:           ModeTextToImage,
		Model:          "agnes-image-2.1-flash",
		Prompt:         "test",
		Size:           "1024x768",
		ResponseFormat: "b64_json",
	}
	_, payload, err := agnesImagePayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if rb, ok := payload["return_base64"].(bool); !ok || !rb {
		t.Errorf("expected return_base64=true for b64_json text_to_image, got %v", payload["return_base64"])
	}
	extra, _ := payload["extra_body"].(map[string]any)
	if extra["response_format"] != "b64_json" {
		t.Errorf("expected b64_json in extra_body, got %v", extra["response_format"])
	}
}

func TestAgnesImagePayloadValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		req  ImagineRequest
	}{
		{"wrong provider", ImagineRequest{Provider: ProviderAgnes, Model: "grok-imagine-image-quality", Mode: ModeTextToImage, Prompt: "test"}},
		{"wrong media type model", ImagineRequest{Provider: ProviderAgnes, Model: "agnes-video-v2.0", Mode: ModeTextToImage, Prompt: "test"}},
		{"deprecated model", ImagineRequest{Provider: ProviderAgnes, Model: "agnes-image-1.2", Mode: ModeTextToImage, Prompt: "test", Size: "1024x768"}},
		{"bad reference count 2.1", ImagineRequest{Provider: ProviderAgnes, Model: "agnes-image-2.1-flash", Mode: ModeMultiImageEdit, Prompt: "test", Size: "1024x768",
			Images: []ImageInput{{URL: "a.png"}, {URL: "b.png"}}}},
		{"missing references for i2i", ImagineRequest{Provider: ProviderAgnes, Model: "agnes-image-2.1-flash", Mode: ModeImageToImage, Prompt: "test", Size: "1024x768"}},
		{"unsupported mode", ImagineRequest{Provider: ProviderAgnes, Model: "agnes-image-2.1-flash", Mode: ModeMultiImageEdit, Prompt: "test", Size: "1024x768"}},
		{"invalid size", ImagineRequest{Provider: ProviderAgnes, Model: "agnes-image-2.1-flash", Mode: ModeTextToImage, Prompt: "test", Size: "1024"}},
	}
	for _, c := range cases {
		c.req.ResponseFormat = "url"
		_, _, err := agnesImagePayload(c.req)
		if err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestAgnesImageResponseParse(t *testing.T) {
	raw := `{
		"created": 1718000000,
		"data": [
			{"url": "https://cdn.agnes.example.com/img1.png", "revised_prompt": "A beautiful scene"},
			{"b64_json": "iVBORw0KGgo=", "mime_type": "image/png"}
		],
		"usage": {"steps": 28}
	}`
	c := NewAgnesClient(agnesBaseURL, nil)
	_ = c
	var resp agnesImageResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("json unmarshal failed: %v", err)
	}
	if len(resp.Data) != 2 {
		t.Fatalf("expected 2 items, got %d", len(resp.Data))
	}
	if resp.Data[0].URL == "" || resp.Data[0].RevisedPrompt == "" {
		t.Error("first item url/revised_prompt missing")
	}
	if resp.Data[1].B64JSON != "iVBORw0KGgo=" || resp.Data[1].MimeType != "image/png" {
		t.Error("second item b64/mime missing")
	}
	if resp.Created != 1718000000 {
		t.Error("created timestamp wrong")
	}
	if steps, ok := resp.Usage["steps"].(float64); !ok || steps != 28 {
		t.Error("usage steps wrong")
	}
	errRaw := `{"error": {"message": "Invalid API key", "code": "auth_error"}}`
	var errResp agnesImageResponse
	if err := json.Unmarshal([]byte(errRaw), &errResp); err != nil {
		t.Fatalf("error json unmarshal failed: %v", err)
	}
	if errResp.Error == nil || errResp.Error.Message != "Invalid API key" {
		t.Error("error response parse failed")
	}
}

func TestAgnesVideoPayloadTextToVideo(t *testing.T) {
	req := VideoRequest{
		Provider: ProviderAgnes,
		Mode:     VideoModeTextToVideo,
		Model:    "agnes-video-v2.0",
		Prompt:   "A cat walking on a sunny beach",
		Parameters: VideoParameters{
			Width:     1152,
			Height:    768,
			NumFrames: 121,
			FrameRate: 24,
		},
	}
	endpoint, payload, err := agnesVideoPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if endpoint != agnesVideosEndpoint {
		t.Errorf("endpoint: got %s", endpoint)
	}
	if payload["model"] != "agnes-video-v2.0" {
		t.Errorf("model: got %v", payload["model"])
	}
	if payload["prompt"] != req.Prompt {
		t.Error("prompt mismatch")
	}
	if payload["width"].(int) != 1152 || payload["height"].(int) != 768 {
		t.Errorf("dimensions wrong")
	}
	if payload["num_frames"].(int) != 121 || payload["frame_rate"].(int) != 24 {
		t.Errorf("frames/rate wrong")
	}
	if _, hasImg := payload["image"]; hasImg {
		t.Error("text_to_video should not have top-level image")
	}
	if _, hasExtra := payload["extra_body"]; hasExtra {
		t.Error("text_to_video should not need extra_body")
	}
}

func TestAgnesVideoPayloadImageToVideo(t *testing.T) {
	req := VideoRequest{
		Provider: ProviderAgnes,
		Mode:     VideoModeImageToVideo,
		Model:    "agnes-video-v2.0",
		Prompt:   "Make this portrait blink softly",
		Parameters: VideoParameters{
			Width:     768,
			Height:    1152,
			NumFrames: 81,
			FrameRate: 24,
		},
		Images: []ImageInput{
			{URL: "https://example.com/portrait.png", SourceType: "url"},
		},
	}
	_, payload, err := agnesVideoPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if payload["image"] != "https://example.com/portrait.png" {
		t.Errorf("top-level image field: got %v", payload["image"])
	}
}

func TestAgnesVideoPayloadMultiImage(t *testing.T) {
	req := VideoRequest{
		Provider: ProviderAgnes,
		Mode:     VideoModeMultiImageVideo,
		Model:    "agnes-video-v2.0",
		Prompt:   "Animate the transition between these scenes",
		Parameters: VideoParameters{
			Width:     1152,
			Height:    768,
			NumFrames: 121,
			FrameRate: 24,
		},
		Images: []ImageInput{
			{URL: "https://example.com/scene1.png"},
			{URL: "https://example.com/scene2.png"},
			{URL: "https://example.com/scene3.png"},
		},
	}
	_, payload, err := agnesVideoPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	extra, ok := payload["extra_body"].(map[string]any)
	if !ok {
		t.Fatal("expected extra_body for multi_image")
	}
	imgs, ok := extra["image"].([]string)
	if !ok || len(imgs) != 3 {
		t.Fatalf("expected 3 images in extra_body.image")
	}
	if mode, _ := extra["mode"].(string); mode != "" {
		t.Errorf("multi_image should not set extra_body.mode, got %q", mode)
	}
}

func TestAgnesVideoPayloadKeyframes(t *testing.T) {
	req := VideoRequest{
		Provider: ProviderAgnes,
		Mode:     VideoModeKeyframes,
		Model:    "agnes-video-v2.0",
		Prompt:   "Camera dolly forward through keyframes",
		Parameters: VideoParameters{
			Width:     1152,
			Height:    768,
			NumFrames: 241,
			FrameRate: 24,
			Seed:      42,
		},
		Images: []ImageInput{
			{URL: "https://example.com/kf1.png"},
			{URL: "https://example.com/kf2.png"},
		},
	}
	_, payload, err := agnesVideoPayload(req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	extra, ok := payload["extra_body"].(map[string]any)
	if !ok {
		t.Fatal("expected extra_body for keyframes")
	}
	if mode, _ := extra["mode"].(string); mode != "keyframes" {
		t.Errorf("expected extra_body.mode=keyframes, got %q", mode)
	}
	imgs, ok := extra["image"].([]string)
	if !ok || len(imgs) != 2 {
		t.Fatalf("expected 2 keyframe images")
	}
	if payload["seed"].(int) != 42 {
		t.Errorf("seed should propagate to top level, got %v", payload["seed"])
	}
}

func TestAgnesVideoPayloadValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		req  VideoRequest
	}{
		{"wrong model", VideoRequest{Provider: ProviderAgnes, Mode: VideoModeTextToVideo, Model: "grok-imagine", Prompt: "test"}},
		{"unsupported mode", VideoRequest{Provider: ProviderAgnes, Mode: ModeMultiImageEdit, Model: "agnes-video-v2.0", Prompt: "test",
			Parameters: VideoParameters{Width: 1152, Height: 768, NumFrames: 121, FrameRate: 24}}},
		{"bad numframes", VideoRequest{Provider: ProviderAgnes, Mode: VideoModeTextToVideo, Model: "agnes-video-v2.0", Prompt: "test",
			Parameters: VideoParameters{Width: 1152, Height: 768, NumFrames: 100, FrameRate: 24}}},
		{"keyframe missing refs", VideoRequest{Provider: ProviderAgnes, Mode: VideoModeKeyframes, Model: "agnes-video-v2.0", Prompt: "test",
			Parameters: VideoParameters{Width: 1152, Height: 768, NumFrames: 121, FrameRate: 24},
			Images: []ImageInput{{URL: "a.png"}}}},
		{"numframes exceeds max", VideoRequest{Provider: ProviderAgnes, Mode: VideoModeTextToVideo, Model: "agnes-video-v2.0", Prompt: "test",
			Parameters: VideoParameters{Width: 1152, Height: 768, NumFrames: 500, FrameRate: 24}}},
		{"bad framerate", VideoRequest{Provider: ProviderAgnes, Mode: VideoModeTextToVideo, Model: "agnes-video-v2.0", Prompt: "test",
			Parameters: VideoParameters{Width: 1152, Height: 768, NumFrames: 121, FrameRate: 100}}},
	}
	for _, c := range cases {
		_, _, err := agnesVideoPayload(c.req)
		if err == nil {
			t.Errorf("%s: expected error, got nil", c.name)
		}
	}
}

func TestAgnesVideoStatusNormalization(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"queued", "queued"},
		{"PENDING", "queued"},
		{"in_progress", "running"},
		{"processing", "running"},
		{"Completed", "completed"},
		{"succeeded", "completed"},
		{"FAILED", "failed"},
		{"error", "failed"},
		{"timed_out", "interrupted"},
		{"something_unknown", "unknown"},
		{"", "unknown"},
	}
	for i, c := range cases {
		got := normalizeAgnesVideoStatus(c.raw)
		if got != c.want {
			t.Errorf("case %d: raw=%q: got %q, want %q", i, c.raw, got, c.want)
		}
	}
}

func TestAgnesVideoPollParsing(t *testing.T) {
	c := NewAgnesClient(agnesBaseURL, nil)
	_ = c
	completedRaw := `{
		"task_id": "task_abc123",
		"video_id": "vid_def456",
		"status": "completed",
		"progress": 100,
		"remixed_from_video_id": "https://cdn.agnes.example.com/output/video_1234.mp4",
		"width": 1152,
		"height": 768,
		"num_frames": 121,
		"frame_rate": 24,
		"size_bytes": 15432100
	}`
	var p agnesVideoPollResponse
	if err := json.Unmarshal([]byte(completedRaw), &p); err != nil {
		t.Fatalf("unmarshal completed failed: %v", err)
	}
	result := parseAgnesVideoPoll(p, "secret_key", "task_abc123", "vid_def456")
	if result.Status != "completed" {
		t.Errorf("status: got %s, want completed", result.Status)
	}
	if result.Progress != 100 {
		t.Errorf("progress: got %d", result.Progress)
	}
	if result.VideoURL != "https://cdn.agnes.example.com/output/video_1234.mp4" {
		t.Errorf("videoURL wrong: got %s", result.VideoURL)
	}
	if result.Width != 1152 || result.Height != 768 {
		t.Error("dimensions wrong")
	}
	if result.NumFrames != 121 || result.FrameRate != 24 {
		t.Error("frames/rate wrong")
	}
	if result.Seconds < 5.0 || result.Seconds > 5.05 {
		t.Errorf("seconds wrong: got %f (121/24=5.04)", result.Seconds)
	}
	if result.SizeBytes != 15432100 {
		t.Errorf("size_bytes: got %d", result.SizeBytes)
	}
	if result.ProviderTaskID != "task_abc123" || result.ProviderVideoID != "vid_def456" {
		t.Error("ids not propagated")
	}

	inProgressRaw := `{"status": "in_progress", "progress": "42"}`
	var ip agnesVideoPollResponse
	_ = json.Unmarshal([]byte(inProgressRaw), &ip)
	res2 := parseAgnesVideoPoll(ip, "", "", "")
	if res2.Status != "running" || res2.Progress != 42 {
		t.Errorf("in_progress parse: status=%s progress=%d", res2.Status, res2.Progress)
	}

	errorRaw := `{"status": "failed", "error": {"message": "Prompt contains banned phrase"}}`
	var er agnesVideoPollResponse
	_ = json.Unmarshal([]byte(errorRaw), &er)
	res3 := parseAgnesVideoPoll(er, "", "", "")
	if res3.Status != "failed" {
		t.Errorf("failed status: got %s", res3.Status)
	}
	if res3.ErrorMessage != "Prompt contains banned phrase" {
		t.Errorf("error message: got %s", res3.ErrorMessage)
	}

	queuedRaw := `{"status": "QUEUED", "progress": 0, "task_id": "task_z", "video_id": "vid_z"}`
	var qr agnesVideoPollResponse
	_ = json.Unmarshal([]byte(queuedRaw), &qr)
	res4 := parseAgnesVideoPoll(qr, "", "", "")
	if res4.Status != "queued" {
		t.Errorf("queued status: got %s", res4.Status)
	}
	if res4.ProviderTaskID != "task_z" || res4.ProviderVideoID != "vid_z" {
		t.Error("ids fallback wrong in queued response")
	}
}

func TestAgnesVideoCreateParsing(t *testing.T) {
	c := NewAgnesClient(agnesBaseURL, nil)
	_ = c
	successRaw := `{"task_id": "task_new1", "video_id": "vid_new2", "status": "queued"}`
	var r agnesVideoCreateResponse
	if err := json.Unmarshal([]byte(successRaw), &r); err != nil {
		t.Fatalf("unmarshal create failed: %v", err)
	}
	if r.TaskID != "task_new1" || r.VideoID != "vid_new2" || r.Status != "queued" {
		t.Errorf("create parse: got task=%s video=%s status=%s", r.TaskID, r.VideoID, r.Status)
	}
}

func TestVideoModeLabels(t *testing.T) {
	cases := map[string]string{
		VideoModeTextToVideo:     "文生视频",
		VideoModeImageToVideo:    "图生视频",
		VideoModeMultiImageVideo: "多图视频",
		VideoModeKeyframes:       "关键帧动画",
		"unknown_mode":           "unknown_mode",
	}
	for mode, want := range cases {
		got := ModeLabel(mode)
		if got != want {
			t.Errorf("mode=%s: got %q, want %q", mode, got, want)
		}
	}
}
