package decoder

/*
#cgo pkg-config: libavcodec libavutil libswscale
#include <libavcodec/avcodec.h>
#include <libavutil/imgutils.h>
#include <libavutil/error.h>
#include <libavutil/log.h>
#include <libswscale/swscale.h>
#include <stdlib.h>
#include <errno.h>

static const AVCodec* find_h264_decoder() {
    return avcodec_find_decoder(AV_CODEC_ID_H264);
}

static void set_log_level(int level) {
    av_log_set_level(level);
}

// receive_all moves every frame the decoder has ready into out, keeping only
// the newest. It returns 1 if out holds a new frame, 0 if none was ready, or a
// negative AVERROR.
static int receive_all(AVCodecContext *ctx, AVFrame *tmp, AVFrame *out) {
    int got = 0;
    for (;;) {
        int ret = avcodec_receive_frame(ctx, tmp);
        if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) {
            return got;
        }
        if (ret < 0) {
            return ret;
        }
        av_frame_unref(out);
        av_frame_move_ref(out, tmp);
        got = 1;
    }
}

// decode sends one packet and collects the frames it produces into out (see
// receive_all). If the decoder refuses input because output is pending, the
// output is drained and the packet sent again, so no input is dropped.
static int decode(AVCodecContext *ctx, AVPacket *pkt, AVFrame *tmp, AVFrame *out) {
    int got = 0;
    int ret = avcodec_send_packet(ctx, pkt);
    if (ret == AVERROR(EAGAIN)) {
        got = receive_all(ctx, tmp, out);
        if (got < 0) {
            return got;
        }
        ret = avcodec_send_packet(ctx, pkt);
    }
    if (ret < 0) {
        return ret;
    }
    ret = receive_all(ctx, tmp, out);
    if (ret < 0) {
        return ret;
    }
    return got | ret;
}

// scale_to converts src into a single-plane RGBA buffer owned by the caller.
static int scale_to(struct SwsContext *sws, const AVFrame *src, uint8_t *dst, int dst_stride) {
    uint8_t *dst_data[4] = {dst, NULL, NULL, NULL};
    int dst_linesize[4] = {dst_stride, 0, 0, 0};
    return sws_scale(sws, (const uint8_t *const *)src->data, src->linesize, 0, src->height,
                     dst_data, dst_linesize);
}
*/
import "C"
import (
	"errors"
	"fmt"
	"image"
	"log/slog"
	"unsafe"
)

// maxPixels is the largest frame the decoder accepts: H.264 Level 6.2's
// maximum frame size of 139,264 macroblocks.
const maxPixels = 139264 * 16 * 16

// errDecoderClosed is returned by DecodeFrame after Close.
var errDecoderClosed = errors.New("H.264 decoder is closed")

// H264Decoder decodes H.264 Annex-B data into RGBA images with FFmpeg.
// It is not safe for concurrent use: one goroutine should own it, decode
// with it and close it.
type H264Decoder struct {
	codecCtx *C.AVCodecContext
	frame    *C.AVFrame // receive buffer
	latest   *C.AVFrame // newest decoded frame
	swsCtx   *C.struct_SwsContext
	packet   *C.AVPacket

	// Input buffer in C memory, reused across calls. FFmpeg requires
	// AV_INPUT_BUFFER_PADDING_SIZE zeroed bytes after the data.
	input    *C.uint8_t
	inputCap int

	// Source format the conversion context was made for, and the output
	// size it scales to.
	width, height int
	format        C.int
	ctxOutW       int
	ctxOutH       int

	// Requested output size; 0 means the stream's own size.
	outW, outH int
}

// SetOutputSize makes DecodeFrame scale frames to w×h pixels (0, 0: the
// stream's own size). Scaling happens in the YUV→RGBA conversion, so a small
// output costs less than a full-size frame.
func (d *H264Decoder) SetOutputSize(w, h int) {
	d.outW, d.outH = w, h
}

func NewH264Decoder() (*H264Decoder, error) {
	return NewH264DecoderWithVerbosity(0)
}

func NewH264DecoderWithVerbosity(verbosity int) (*H264Decoder, error) {
	// Set FFmpeg log level based on verbosity
	// AV_LOG_QUIET = -8, AV_LOG_ERROR = 16, AV_LOG_WARNING = 24, AV_LOG_INFO = 32
	var logLevel C.int
	switch verbosity {
	case 0:
		logLevel = -8 // AV_LOG_QUIET - suppress all logs
	case 1:
		logLevel = 16 // AV_LOG_ERROR - only errors
	default:
		logLevel = 24 // AV_LOG_WARNING - errors and warnings
	}
	C.set_log_level(logLevel)

	codec := C.find_h264_decoder()
	if codec == nil {
		return nil, errors.New("H.264 decoder not found")
	}

	d := &H264Decoder{}
	if err := d.init(codec); err != nil {
		d.Close()
		return nil, err
	}

	slog.Debug("H.264 decoder created")
	return d, nil
}

// init allocates the FFmpeg state. On error the caller closes d, which frees
// whatever was allocated.
func (d *H264Decoder) init(codec *C.AVCodec) error {
	d.codecCtx = C.avcodec_alloc_context3(codec)
	if d.codecCtx == nil {
		return errors.New("failed to allocate codec context")
	}
	// Output each frame as soon as it is decoded instead of holding one back
	// for possible reordering: live video, and WebRTC H.264 has no B-frames.
	d.codecCtx.flags |= C.AV_CODEC_FLAG_LOW_DELAY
	// FFmpeg otherwise accepts any size an SPS declares: a broken or hostile
	// stream could make every frame cost gigabytes.
	d.codecCtx.max_pixels = maxPixels
	if C.avcodec_open2(d.codecCtx, codec, nil) < 0 {
		return errors.New("failed to open codec")
	}

	d.frame = C.av_frame_alloc()
	d.latest = C.av_frame_alloc()
	if d.frame == nil || d.latest == nil {
		return errors.New("failed to allocate frames")
	}

	d.packet = C.av_packet_alloc()
	if d.packet == nil {
		return errors.New("failed to allocate packet")
	}
	return nil
}

// DecodeFrame decodes data (one or more NAL units with start codes) and
// returns the newest frame it completed, or nil if the decoder needs more input.
func (d *H264Decoder) DecodeFrame(data []byte) (*image.RGBA, error) {
	if d.codecCtx == nil {
		return nil, errDecoderClosed
	}
	if len(data) == 0 {
		return nil, nil
	}

	if err := d.setInput(data); err != nil {
		return nil, err
	}
	ret := C.decode(d.codecCtx, d.packet, d.frame, d.latest)
	C.av_packet_unref(d.packet) // the data belongs to d.input, not to the packet
	if ret < 0 {
		return nil, fmt.Errorf("error decoding frame: %d", ret)
	}
	if ret == 0 {
		return nil, nil // need more data
	}
	return d.toRGBA()
}

// setInput copies data into the padded C input buffer and points the packet at it.
func (d *H264Decoder) setInput(data []byte) error {
	const padding = C.AV_INPUT_BUFFER_PADDING_SIZE
	need := len(data) + padding
	if need > d.inputCap {
		C.av_free(unsafe.Pointer(d.input))
		d.input = (*C.uint8_t)(C.av_malloc(C.size_t(need)))
		if d.input == nil {
			d.inputCap = 0
			return errors.New("failed to allocate input buffer")
		}
		d.inputCap = need
	}

	buf := unsafe.Slice((*byte)(unsafe.Pointer(d.input)), need)
	copy(buf, data)
	clear(buf[len(data):])

	d.packet.data = d.input
	d.packet.size = C.int(len(data))
	return nil
}

// toRGBA converts the newest decoded frame to a new RGBA image.
func (d *H264Decoder) toRGBA() (*image.RGBA, error) {
	width := int(d.latest.width)
	height := int(d.latest.height)
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid frame dimensions: %dx%d", width, height)
	}

	outW, outH := width, height
	if d.outW > 0 && d.outH > 0 {
		outW, outH = d.outW, d.outH
	}

	// (Re)create the conversion context when the stream's format or the
	// output size changes. SWS_AREA averages the source pixels each output
	// pixel covers, the usual choice for downscaling.
	if d.swsCtx == nil || d.width != width || d.height != height || d.format != d.latest.format ||
		d.ctxOutW != outW || d.ctxOutH != outH {
		if d.width != width || d.height != height {
			slog.Info("H.264: video size", "width", width, "height", height)
		}
		C.sws_freeContext(d.swsCtx)
		d.swsCtx = C.sws_getContext(
			C.int(width), C.int(height), C.enum_AVPixelFormat(d.latest.format),
			C.int(outW), C.int(outH), C.AV_PIX_FMT_RGBA,
			C.SWS_AREA, nil, nil, nil,
		)
		if d.swsCtx == nil {
			return nil, errors.New("failed to create scaling context")
		}
		d.width, d.height, d.format = width, height, d.latest.format
		d.ctxOutW, d.ctxOutH = outW, outH
	}

	// swscale writes straight into the image's pixels. C only uses the Go
	// pointer for the duration of the call, as cgo requires; it is not stored.
	img := newPaddedRGBA(outW, outH)
	if C.scale_to(d.swsCtx, d.latest, (*C.uint8_t)(unsafe.Pointer(&img.Pix[0])), C.int(img.Stride)) < 0 {
		return nil, errors.New("failed to convert frame to RGBA")
	}
	return img, nil
}

// rowPadding is the slack after each row of an image swscale writes into:
// its x86 SIMD code converts several pixels at a time and can write past the
// end of a row (and of the last row, past the end of the buffer).
const rowPadding = 64

// newPaddedRGBA returns a w×h image whose rows are 64-byte aligned and have
// at least rowPadding bytes of slack, so swscale can write into it safely.
func newPaddedRGBA(w, h int) *image.RGBA {
	stride := (4*w + rowPadding + 63) &^ 63
	return &image.RGBA{Pix: make([]byte, stride*h), Stride: stride, Rect: image.Rect(0, 0, w, h)}
}

// Close frees the decoder. It is idempotent and safe on a nil or partially
// initialized decoder. DecodeFrame returns an error afterwards.
func (d *H264Decoder) Close() {
	if d == nil || d.codecCtx == nil && d.frame == nil && d.latest == nil && d.packet == nil {
		return
	}
	C.sws_freeContext(d.swsCtx)
	d.swsCtx = nil
	C.av_free(unsafe.Pointer(d.input))
	d.input, d.inputCap = nil, 0
	C.av_packet_free(&d.packet)
	C.av_frame_free(&d.latest)
	C.av_frame_free(&d.frame)
	C.avcodec_free_context(&d.codecCtx)
	slog.Debug("H.264 decoder closed")
}
