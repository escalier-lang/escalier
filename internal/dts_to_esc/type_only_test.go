package dts_to_esc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// routingLib exercises every rule the type-only analysis applies, in
// one lib.dom.d.ts. `Request` routes to web:fetch and `ReadableStream`
// to web:streams by the §6.1 partition, so the referring declarations
// sit in packages other than the web:dom the rest falls through to.
const routingLib = `
interface Request { sole: SoleOpts; shared: SharedOpts; }
interface ReadableStream { shared: SharedOpts; }
type SoleOpts = string;
type SharedOpts = string;
type Orphan = string;
interface SelfRef { next: SelfRef; }
interface Trio { length: number; }
interface TrioConstructor { new (): Trio; }
declare var Trio: TrioConstructor;
`

// TestAnalyzeTypeOnlyRouting covers the four verdicts the analysis
// reaches over routingLib above.
//
//   - SoleOpts has one referrer outside web:dom, so it reads as
//     misplaced.
//   - SharedOpts has two, which makes it shared vocabulary that belongs
//     in web:dom.
//   - Orphan has none, and SelfRef's only reference is its own, which
//     does not speak for any package.
//   - Trio and TrioConstructor are the two halves of a class, so
//     `declare var Trio` keeps both out of the analysis entirely.
func TestAnalyzeTypeOnlyRouting(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", routingLib)})
	require.NoError(t, err)

	routing := AnalyzeTypeOnlyRouting(res).forPackage(WebDOM.URI)

	require.Equal(t, []SoleReferrer{
		{Name: "SoleOpts", DeclaredIn: "web:dom", ReferencedBy: "web:fetch"},
	}, routing.SoleReferrer)
	require.Equal(t, []UnreferencedDecl{
		{Name: "Orphan", DeclaredIn: "web:dom"},
		{Name: "SelfRef", DeclaredIn: "web:dom"},
	}, routing.Unreferenced)
}

// TestReportTypeOnlyRouting_NamesNothing covers the quiet case: a lib
// whose every type-only declaration is either shared vocabulary or
// already in the package that references it writes no report at all.
func TestReportTypeOnlyRouting_NamesNothing(t *testing.T) {
	t.Parallel()
	res, err := PartitionLib([]LibInput{parseLib(t, "lib.dom.d.ts", `
interface Request { shared: SharedOpts; }
interface ReadableStream { shared: SharedOpts; }
type SharedOpts = string;
`)})
	require.NoError(t, err)

	var b strings.Builder
	require.NoError(t, ReportTypeOnlyRouting(res, &b))
	require.Empty(t, b.String())
}

// TestReportTypeOnlyRouting_PinnedLibSet is the §6.1 gate over the real
// input: every web:dom type-only declaration is referenced by web:dom
// itself or by two or more packages. The snapshot holds what the
// partition has yet to place, so a TypeScript bump that adds a
// type-only companion moves this test rather than absorbing the name
// into web:dom unnoticed.
func TestReportTypeOnlyRouting_PinnedLibSet(t *testing.T) {
	t.Parallel()
	libDir := filepath.Join("..", "..", "node_modules", "typescript", "lib")
	if _, err := os.Stat(libDir); err != nil {
		t.Skipf("TypeScript lib dir not present at %s; run `pnpm install`: %v", libDir, err)
	}
	basenames, err := DiscoverLibFiles(libDir)
	require.NoError(t, err)
	inputs, err := ParseLibFiles(libDir, basenames)
	require.NoError(t, err)
	res, err := PartitionLib(inputs)
	require.NoError(t, err)

	var b strings.Builder
	require.NoError(t, ReportTypeOnlyRouting(res, &b))
	snaps.MatchInlineSnapshot(t, b.String(), snaps.Inline(`  web:dom: 1 type-only decls only web:compression references (CompressionFormat)
  web:dom: 2 type-only decls only web:fetch references (HeadersIterator, RequestPriority)
  web:dom: 1 type-only decls only web:payments references (PaymentOptions)
  web:dom: 1 type-only decls only web:performance references (NavigationTimingType)
  web:dom: 1 type-only decls only web:service_worker references (GetNotificationOptions)
  web:dom: 6 type-only decls only web:streams references (ReadableStreamAsyncIterator, ReadableStreamController, ReadableStreamGetReaderOptions, ReadableStreamIteratorOptions, ReadableStreamReader, ReadableStreamType)
  web:dom: 1 type-only decls only web:url references (URLSearchParamsIterator)
  web:dom: 1 type-only decls only web:web_audio references (AudioTimestamp)
  web:dom: 11 type-only decls only web:web_codecs references (AudioDataOutputCallback, AudioDecoderEventMap, AudioEncoderEventMap, AvcEncoderConfig, EncodedAudioChunkOutputCallback, EncodedVideoChunkOutputCallback, ImageBufferSource, OpusEncoderConfig, VideoDecoderEventMap, VideoEncoderEventMap, VideoFrameOutputCallback)
  web:dom: 3 type-only decls only web:web_rtc references (RTCDtlsRole, RTCLocalSessionDescriptionInit, RTCQualityLimitationReason)
  web:dom: 5 type-only decls only web:webauthn references (AuthenticationExtensionsClientInputs, AuthenticationExtensionsClientOutputs, PublicKeyCredentialClientCapabilities, PublicKeyCredentialCreationOptionsJSON, PublicKeyCredentialRequestOptionsJSON)
  web:dom: 34 type-only decls only web:webgl references (ANGLE_instanced_arrays, EXT_blend_minmax, EXT_color_buffer_float, EXT_color_buffer_half_float, EXT_float_blend, EXT_frag_depth, EXT_sRGB, EXT_shader_texture_lod, EXT_texture_compression_bptc, EXT_texture_compression_rgtc, EXT_texture_filter_anisotropic, KHR_parallel_shader_compile, OES_element_index_uint, OES_fbo_render_mipmap, OES_standard_derivatives, OES_texture_float, OES_texture_float_linear, OES_texture_half_float, OES_texture_half_float_linear, OES_vertex_array_object, OVR_multiview2, WEBGL_color_buffer_float, WEBGL_compressed_texture_astc, WEBGL_compressed_texture_etc, WEBGL_compressed_texture_etc1, WEBGL_compressed_texture_pvrtc, WEBGL_compressed_texture_s3tc, WEBGL_compressed_texture_s3tc_srgb, WEBGL_debug_renderer_info, WEBGL_debug_shaders, WEBGL_depth_texture, WEBGL_draw_buffers, WEBGL_lose_context, WEBGL_multi_draw)
  web:dom: 22 type-only decls nothing references (AutoFillAddressKind, AutoFillContactField, AutoFillContactKind, AutoFillCredentialField, AutoFillField, AutoFillSection, ClientQueryOptions, ClientRect, ClipboardItemData, DisplayCaptureSurfaceType, EXT_texture_norm16, ElementTagNameMap, GPUError, HTMLTableDataCellElement, HTMLTableHeaderCellElement, OES_draw_buffers_indexed, OnBeforeUnloadEventHandler, OptionalPostfixToken, OptionalPrefixToken, RTCCertificateExpiration, StyleMedia, VideoFacingModeEnum)
`))
}
