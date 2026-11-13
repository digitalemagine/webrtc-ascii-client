package client

import "testing"

func TestValidateVideoCodec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		mimeType   string
		supported  bool
		wantStatus string
	}{
		{
			name:      "h264 lowercase is supported",
			mimeType:  "video/h264",
			supported: true,
		},
		{
			name:      "h264 uppercase is supported",
			mimeType:  "video/H264",
			supported: true,
		},
		{
			name:       "vp8 is rejected as H.264 only",
			mimeType:   "video/vp8",
			supported:  false,
			wantStatus: "Codec video/vp8 is not supported (H.264 only).",
		},
		{
			name:       "vp9 uppercase is rejected as H.264 only",
			mimeType:   "video/VP9",
			supported:  false,
			wantStatus: "Codec video/VP9 is not supported (H.264 only).",
		},
		{
			name:       "unknown codec is rejected",
			mimeType:   "video/av1",
			supported:  false,
			wantStatus: "Unsupported codec: video/av1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			status, supported := validateVideoCodec(tt.mimeType)
			if supported != tt.supported {
				t.Fatalf("supported = %v, want %v", supported, tt.supported)
			}
			if status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", status, tt.wantStatus)
			}
		})
	}
}
