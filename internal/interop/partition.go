package interop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/escalier-lang/escalier/internal/set"
)

// Package identifies a target pseudo-package for a TS-lib top-level
// declaration. URI is the import string ("std:array", "web:dom"); File
// is the on-disk relative path under internal/interop/data/ (the
// directory part — "std" or "web" — plus the per-package file basename
// with underscores for multi-word names, e.g. "std/typed_arrays.esc").
//
// See planning/builtins/implementation_plan.md §6.1.
type Package struct {
	URI  string
	File string
}

// Partition routes a TS-lib top-level declaration name to its target
// pseudo-package. Source of truth: the §6.1 partition table.
//
// Lookup order at the routing site (see Route below):
//
//  1. ExplicitDrops — symbols intentionally dropped (`globalThis`,
//     `eval`). The routing site logs and skips emission.
//  2. Partition (this map) — the hand-maintained, full enumeration of
//     std:* and web:* siblings.
//  3. DOMResidual — a `.d.ts` source-file allowlist (lib.dom.d.ts and
//     its DOM-coupled siblings). Any unmapped name that originates in
//     one of these files routes to `web:dom`. This is the catch-all
//     for the DOM mass, kept as a file allowlist rather than a per-
//     symbol map because lib.dom.d.ts contains thousands of element
//     classes, interfaces, and event types that all share one target
//     package per §4.2 (single-`web:dom` package).
//  4. Otherwise — the unmapped-symbol fail-safe trips (see §6.1).
//
// Keys are the bare TS declaration name as it appears at the top level
// of the `.d.ts` file (no namespace qualification — namespace contents
// are partitioned by their member name after flattening).
var Partition = map[string]Package{}

// Std packages, per the §6.1 table.
var stdPackages = []struct {
	URI     string
	File    string
	Members []string
}{
	{"std:array", "std/array.esc", []string{
		"Array", "ArrayConstructor",
		"ReadonlyArray", "ConcatArray", "ArrayLike",
		"ArrayIterator",
	}},
	{"std:string", "std/string.esc", []string{
		"String", "StringConstructor",
		"TemplateStringsArray",
		"StringIterator",
	}},
	{"std:number", "std/number.esc", []string{
		"Number", "NumberConstructor",
		"parseInt", "parseFloat", "isNaN", "isFinite",
		"NaN", "Infinity",
	}},
	{"std:boolean", "std/boolean.esc", []string{
		"Boolean", "BooleanConstructor",
	}},
	{"std:bigint", "std/bigint.esc", []string{
		"BigInt", "BigIntConstructor",
	}},
	{"std:regexp", "std/regexp.esc", []string{
		"RegExp", "RegExpConstructor",
		"RegExpMatchArray", "RegExpExecArray",
		"RegExpStringIterator",
	}},
	{"std:symbol", "std/symbol.esc", []string{
		"Symbol", "SymbolConstructor",
	}},
	{"std:object", "std/object.esc", []string{
		"Object", "ObjectConstructor",
		"PropertyDescriptor", "PropertyDescriptorMap",
		"TypedPropertyDescriptor",
		"Partial", "Required", "Readonly", "Pick", "Omit", "Record",
		"Exclude", "Extract", "NonNullable",
		"PropertyKey",
	}},
	{"std:function", "std/function.esc", []string{
		"Function", "FunctionConstructor", "CallableFunction",
		"NewableFunction", "IArguments",
		"Parameters", "ConstructorParameters", "ReturnType",
		"InstanceType", "ThisParameterType", "OmitThisParameter",
		"ThisType",
	}},
	{"std:date", "std/date.esc", []string{
		"Date", "DateConstructor",
	}},
	{"std:map", "std/map.esc", []string{
		"Map", "MapConstructor", "ReadonlyMap",
		"WeakMap", "WeakMapConstructor",
		"MapIterator",
	}},
	{"std:set", "std/set.esc", []string{
		"Set", "SetConstructor", "ReadonlySet",
		"WeakSet", "WeakSetConstructor",
		"SetIterator",
	}},
	{"std:weak_ref", "std/weak_ref.esc", []string{
		"WeakRef", "WeakRefConstructor",
		"FinalizationRegistry", "FinalizationRegistryConstructor",
		"WeakKey", "WeakKeyTypes",
	}},
	{"std:iterator", "std/iterator.esc", []string{
		"Iterator", "Iterable", "IterableIterator",
		"IteratorResult", "IteratorYieldResult", "IteratorReturnResult",
		"IteratorObject",
		"BuiltinIteratorReturn",
		"Generator", "GeneratorFunction", "GeneratorFunctionConstructor",
	}},
	{"std:async", "std/async.esc", []string{
		"Promise", "PromiseConstructor", "PromiseLike",
		"PromiseFulfilledResult", "PromiseRejectedResult", "PromiseSettledResult",
		"Awaited",
		"AsyncIterator", "AsyncIterable", "AsyncIterableIterator",
		"AsyncIteratorObject",
		"AsyncGenerator", "AsyncGeneratorFunction", "AsyncGeneratorFunctionConstructor",
		"AggregateError", "AggregateErrorConstructor",
		"PromiseConstructorLike",
	}},
	{"std:error", "std/error.esc", []string{
		"Error", "ErrorConstructor",
		"TypeError", "TypeErrorConstructor",
		"RangeError", "RangeErrorConstructor",
		"SyntaxError", "SyntaxErrorConstructor",
		"ReferenceError", "ReferenceErrorConstructor",
		"ErrorOptions", "ErrorCause",
	}},
	{"std:url", "std/url.esc", []string{
		"URIError", "URIErrorConstructor",
		"encodeURI", "decodeURI",
		"encodeURIComponent", "decodeURIComponent",
	}},
	{"std:math", "std/math.esc", []string{
		"Math",
	}},
	{"std:json", "std/json.esc", []string{
		"JSON",
	}},
	{"std:console", "std/console.esc", []string{
		"Console", "console",
	}},
	{"std:typed_arrays", "std/typed_arrays.esc", []string{
		"ArrayBuffer", "ArrayBufferConstructor", "ArrayBufferLike",
		"ArrayBufferTypes", "ArrayBufferView",
		"SharedArrayBuffer", "SharedArrayBufferConstructor",
		"DataView", "DataViewConstructor",
		"Int8Array", "Int8ArrayConstructor",
		"Uint8Array", "Uint8ArrayConstructor",
		"Uint8ClampedArray", "Uint8ClampedArrayConstructor",
		"Int16Array", "Int16ArrayConstructor",
		"Uint16Array", "Uint16ArrayConstructor",
		"Int32Array", "Int32ArrayConstructor",
		"Uint32Array", "Uint32ArrayConstructor",
		"Float16Array", "Float16ArrayConstructor",
		"Float32Array", "Float32ArrayConstructor",
		"Float64Array", "Float64ArrayConstructor",
		"BigInt64Array", "BigInt64ArrayConstructor",
		"BigUint64Array", "BigUint64ArrayConstructor",
		"Atomics",
	}},
	{"std:reflect", "std/reflect.esc", []string{
		"Reflect",
	}},
	{"std:proxy", "std/proxy.esc", []string{
		"Proxy", "ProxyConstructor",
		"ProxyHandler", "ProxyHandlerStatic",
	}},
	{"std:intl", "std/intl.esc", []string{
		"Intl",
	}},
	{"std:temporal", "std/temporal.esc", []string{
		"Temporal",
	}},
	{"std:wasm", "std/wasm.esc", []string{
		"WebAssembly",
	}},
}

// Standalone web sibling packages, per the §6.1 table. The DOM mass
// (lib.dom.d.ts symbols not enumerated here) routes to web:dom via
// DOMResidualSources below.
//
// MDN's Web API index (https://developer.mozilla.org/en-US/docs/Web/API)
// lists many APIs that don't get a dedicated web:* package here.
// Excluding experimental and deprecated entries, the uncovered set is
// split into two groups:
//
//  1. Absorbed by the web:dom catch-all because their interfaces are
//     declared in lib.dom.d.ts (per §4.2 the entire DOM/HTML/CSSOM/
//     observer/event tree lives in one package):
//
//     - Beacon API
//     - Broadcast Channel API
//     - Canvas API
//     - Channel Messaging API
//     - Clipboard API
//     - Console API
//     - Credential Management API
//     - CSS Custom Highlight API
//     - CSS Font Loading API
//     - CSS Object Model (CSSOM)
//     - CSSOM view API
//     - Device orientation events
//     - Fullscreen API
//     - Gamepad API
//     - Geolocation API
//     - Geometry interfaces
//     - History API
//     - HTML Drag and Drop API
//     - HTML DOM API
//     - Intersection Observer API
//     - Media Capabilities API
//     - Media Capture and Streams API
//     - Media Session API
//     - Media Source API
//     - MediaStream Image Capture API
//     - MediaStream Recording API
//     - Mutation Observer (part of DOM)
//     - Navigation API
//     - Notifications API
//     - Page Visibility API
//     - Permissions API
//     - Picture-in-Picture API
//     - Pointer events
//     - Pointer Lock API
//     - Popover API
//     - Reporting API
//     - Resize Observer API
//     - Screen Capture API
//     - Screen Orientation API
//     - Screen Wake Lock API
//     - Selection API
//     - Server-sent events
//     - SVG API
//     - Touch events
//     - UI Events
//     - URL Pattern API
//     - Vibration API
//     - View Transition API
//     - Web Animations API
//     - Web Components (custom-element activation deferred per FR7/FR9)
//     - Web Share API
//     - Web Speech API
//     - WebVTT API
//     - XMLHttpRequest API
//
//  2. Not in lib.dom.d.ts today, so no Escalier symbols exist —
//     partition entries will be needed if/when TS ships them:
//
//     - Background Synchronization API
//     - Background Tasks API
//     - Badging API
//     - Battery Status API
//     - Cookie Store API
//     - CSS Properties and Values API
//     - CSS Typed Object Model API
//     - Device Memory API
//     - Document Picture-in-Picture API
//     - Encrypted Media Extensions API
//     - File System API
//     - File and Directory Entries API
//     - HTML Sanitizer API
//     - Houdini APIs
//     - Invoker Commands API
//     - Network Information API
//     - Prioritized Task Scheduling API
//     - Remote Playback API
//     - Sensor APIs
//     - Storage API
//     - Storage Access API
//     - Trusted Types API
//     - URL Fragment Text Directives
//     - Web Locks API
//     - Web MIDI API
//     - Web Serial API
//     - WebTransport API
//
// The unmapped-symbol fail-safe (see Route) catches any group-2 API
// that ships in a future lib bump — the converter aborts with the
// offending name and points at this file.
var webPackages = []struct {
	URI     string
	File    string
	Members []string
}{
	{"web:fetch", "web/fetch.esc", []string{
		"fetch",
		"Request", "RequestInit", "RequestInfo",
		"Response", "ResponseInit", "ResponseType",
		"Headers", "HeadersInit",
		"Body", "BodyInit",
		"ReferrerPolicy",
		"RequestCache", "RequestCredentials", "RequestDestination",
		"RequestMode", "RequestRedirect",
		// FormData / FormDataEntryValue intentionally not listed:
		// MDN classifies them under the XMLHttpRequest API, not
		// Fetch. They are declared in lib.dom.d.ts so they route to
		// web:dom via the residual rule.
	}},
	{"web:streams", "web/streams.esc", []string{
		"ReadableStream", "ReadableStreamDefaultReader",
		"ReadableStreamBYOBReader", "ReadableStreamDefaultController",
		"ReadableByteStreamController", "ReadableStreamBYOBRequest",
		"ReadableStreamGenericReader",
		"ReadableStreamReadResult", "ReadableStreamReadValueResult",
		"ReadableStreamReadDoneResult",
		"ReadableWritablePair",
		"WritableStream", "WritableStreamDefaultWriter",
		"WritableStreamDefaultController",
		"TransformStream", "TransformStreamDefaultController",
		"ByteLengthQueuingStrategy", "CountQueuingStrategy",
		"QueuingStrategy", "QueuingStrategyInit", "QueuingStrategySize",
		"StreamPipeOptions",
		"UnderlyingSource", "UnderlyingByteSource",
		"UnderlyingDefaultSource", "UnderlyingSourceCancelCallback",
		"UnderlyingSourcePullCallback", "UnderlyingSourceStartCallback",
		"UnderlyingSink", "UnderlyingSinkAbortCallback",
		"UnderlyingSinkCloseCallback", "UnderlyingSinkStartCallback",
		"UnderlyingSinkWriteCallback",
		"Transformer", "TransformerFlushCallback",
		"TransformerStartCallback", "TransformerTransformCallback",
		"TransformerCancelCallback",
		"GenericTransformStream",
	}},
	{"web:compression", "web/compression.esc", []string{
		// MDN documents the Compression Streams API as its own API
		// distinct from Streams: https://developer.mozilla.org/en-US/docs/Web/API/Compression_Streams_API
		"CompressionStream", "DecompressionStream",
	}},
	{"web:crypto", "web/crypto.esc", []string{
		"crypto", "Crypto", "SubtleCrypto", "CryptoKey", "CryptoKeyPair",
		"AesCbcParams", "AesCfbParams", "AesCmacParams", "AesCtrParams",
		"AesDerivedKeyParams", "AesGcmParams", "AesKeyAlgorithm",
		"AesKeyGenParams",
		"Algorithm", "AlgorithmIdentifier",
		"BigInteger",
		"DhKeyAlgorithm", "DhKeyDeriveParams", "DhKeyGenParams",
		"EcKeyAlgorithm", "EcKeyGenParams", "EcKeyImportParams",
		"EcdhKeyDeriveParams", "EcdsaParams",
		"HkdfParams", "HmacImportParams", "HmacKeyAlgorithm",
		"HmacKeyGenParams",
		"JsonWebKey",
		"KeyAlgorithm", "KeyFormat", "KeyType", "KeyUsage",
		"NamedCurve",
		"Pbkdf2Params",
		"RsaHashedImportParams", "RsaHashedKeyAlgorithm",
		"RsaHashedKeyGenParams", "RsaKeyAlgorithm", "RsaKeyGenParams",
		"RsaOaepParams", "RsaOtherPrimesInfo", "RsaPssParams",
		"HashAlgorithmIdentifier",
		// BufferSource is a general WebIDL typedef
		// (ArrayBuffer | ArrayBufferView) used by Fetch, Streams,
		// WebSocket, TextDecoder, WebGL, Crypto, …; it routes to
		// web:dom via the residual rule (it is declared in
		// lib.dom.d.ts) rather than being pinned to any one API.
	}},
	{"web:workers", "web/workers.esc", []string{
		"Worker", "WorkerOptions", "WorkerType",
		"SharedWorker",
		"WorkerGlobalScope", "DedicatedWorkerGlobalScope",
		"SharedWorkerGlobalScope",
		"WorkerLocation", "WorkerNavigator",
		"AbstractWorker", "WorkerEventMap",
	}},
	{"web:webgl", "web/webgl.esc", []string{
		"WebGLRenderingContext", "WebGLRenderingContextBase",
		"WebGLRenderingContextOverloads",
		"WebGL2RenderingContext", "WebGL2RenderingContextBase",
		"WebGL2RenderingContextOverloads",
		"WebGLActiveInfo", "WebGLBuffer", "WebGLContextEvent",
		"WebGLContextEventInit", "WebGLContextAttributes",
		"WebGLFramebuffer", "WebGLProgram", "WebGLQuery",
		"WebGLRenderbuffer", "WebGLSampler", "WebGLShader",
		"WebGLShaderPrecisionFormat", "WebGLSync", "WebGLTexture",
		"WebGLTransformFeedback", "WebGLUniformLocation",
		"WebGLVertexArrayObject", "WebGLObject",
		"WebGLPowerPreference",
		"GLbitfield", "GLboolean", "GLbyte", "GLclampf", "GLenum",
		"GLfloat", "GLint", "GLint64", "GLintptr", "GLshort",
		"GLsizei", "GLsizeiptr", "GLubyte", "GLuint", "GLuint64",
		"GLushort", "GLuint64EXT", "GLint64EXT",
		"TexImageSource",
		"Float32List", "Int32List", "Uint32List",
	}},
	{"web:web_audio", "web/web_audio.esc", []string{
		// Symbols MDN documents under Web Audio that are absent from
		// the pinned lib.dom.d.ts (no partition entry needed today):
		// AudioWorkletProcessor, AudioWorkletGlobalScope,
		// AudioPlaybackStats, ScriptProcessorNode (legacy). Add here
		// if a future TS version bump ships them.
		"AudioContext", "AudioContextOptions", "AudioContextState",
		"AudioContextLatencyCategory",
		"AudioBuffer", "AudioBufferOptions", "AudioBufferSourceNode",
		"AudioBufferSourceOptions",
		"AudioDestinationNode", "AudioListener", "AudioNode",
		"AudioNodeOptions", "AudioParam", "AudioParamMap",
		"AudioParamDescriptor",
		"AudioProcessingEvent", "AudioProcessingEventInit",
		"AudioScheduledSourceNode", "AudioScheduledSourceNodeEventMap",
		"AudioWorklet", "AudioWorkletNode", "AudioWorkletNodeOptions",
		"AudioWorkletNodeEventMap",
		"AnalyserNode", "AnalyserOptions",
		"BaseAudioContext", "BaseAudioContextEventMap",
		"BiquadFilterNode", "BiquadFilterOptions", "BiquadFilterType",
		"ChannelMergerNode", "ChannelMergerOptions",
		"ChannelSplitterNode", "ChannelSplitterOptions",
		"ChannelCountMode", "ChannelInterpretation",
		"ConstantSourceNode", "ConstantSourceOptions",
		"ConvolverNode", "ConvolverOptions",
		"DelayNode", "DelayOptions",
		"DynamicsCompressorNode", "DynamicsCompressorOptions",
		"GainNode", "GainOptions",
		"IIRFilterNode", "IIRFilterOptions",
		"MediaElementAudioSourceNode", "MediaElementAudioSourceOptions",
		"MediaStreamAudioDestinationNode",
		"MediaStreamAudioSourceNode", "MediaStreamAudioSourceOptions",
		"MediaStreamTrackAudioSourceNode", "MediaStreamTrackAudioSourceOptions",
		"OfflineAudioCompletionEvent", "OfflineAudioCompletionEventInit",
		"OfflineAudioContext", "OfflineAudioContextEventMap",
		"OfflineAudioContextOptions",
		"OscillatorNode", "OscillatorOptions", "OscillatorType",
		"PannerNode", "PannerOptions", "PanningModelType",
		"DistanceModelType",
		"PeriodicWave", "PeriodicWaveConstraints", "PeriodicWaveOptions",
		"StereoPannerNode", "StereoPannerOptions",
		"WaveShaperNode", "WaveShaperOptions", "OverSampleType",
		"AutomationRate",
		"DecodeErrorCallback", "DecodeSuccessCallback",
	}},
	{"web:web_rtc", "web/web_rtc.esc", []string{
		// Symbols MDN documents under WebRTC that are absent from the
		// pinned lib.dom.d.ts (no partition entry needed today):
		// RTCDTMFSender, RTCDTMFToneChangeEvent,
		// RTCDTMFToneChangeEventInit, RTCIdentityAssertion,
		// RTCIdentityProvider, RTCIdentityProviderRegistrar,
		// RTCTransformEvent, RTCRtpScriptTransformer, and the
		// per-source/codec stats variants (RTCAudioSourceStats,
		// RTCVideoSourceStats, RTCCodecStats, RTCIceCandidateStats,
		// RTCPeerConnectionStats). Add here if a future TS version
		// bump ships them.
		"RTCPeerConnection", "RTCPeerConnectionEventMap",
		"RTCPeerConnectionIceErrorEvent", "RTCPeerConnectionIceErrorEventInit",
		"RTCPeerConnectionIceEvent", "RTCPeerConnectionIceEventInit",
		"RTCDataChannel", "RTCDataChannelEvent", "RTCDataChannelEventInit",
		"RTCDataChannelEventMap", "RTCDataChannelInit", "RTCDataChannelState",
		"RTCRtpSender", "RTCRtpReceiver", "RTCRtpTransceiver",
		"RTCRtpTransceiverInit", "RTCRtpTransceiverDirection",
		"RTCRtpCapabilities", "RTCRtpCodec", "RTCRtpCodecCapability",
		"RTCRtpCodecParameters", "RTCRtpCodingParameters",
		"RTCRtpContributingSource", "RTCRtpEncodingParameters",
		"RTCRtpHeaderExtensionCapability", "RTCRtpHeaderExtensionParameters",
		"RTCRtpParameters", "RTCRtpReceiveParameters",
		"RTCRtpScriptTransform", "RTCRtpSendParameters", "RTCRtpStreamStats",
		"RTCRtpSynchronizationSource", "RTCRtpTransform",
		"RTCRtcpParameters",
		"RTCIceCandidate", "RTCIceCandidateInit", "RTCIceCandidatePair",
		"RTCIceCandidatePairStats", "RTCIceCandidateType",
		"RTCIceComponent", "RTCIceConnectionState", "RTCIceGathererState",
		"RTCIceGatheringState", "RTCIceParameters", "RTCIceProtocol",
		"RTCIceRole", "RTCIceServer", "RTCIceTcpCandidateType",
		"RTCIceTransport", "RTCIceTransportEventMap", "RTCIceTransportPolicy",
		"RTCIceTransportState",
		"RTCDtlsTransport", "RTCDtlsTransportEventMap", "RTCDtlsTransportState",
		"RTCSctpTransport", "RTCSctpTransportEventMap", "RTCSctpTransportState",
		"RTCSessionDescription", "RTCSessionDescriptionInit",
		"RTCSdpType",
		"RTCTrackEvent", "RTCTrackEventInit",
		"RTCError", "RTCErrorEvent", "RTCErrorEventInit", "RTCErrorInit",
		"RTCErrorDetailType",
		"RTCStats", "RTCStatsReport", "RTCStatsType", "RTCStatsIceCandidatePairState",
		"RTCAnswerOptions", "RTCBundlePolicy", "RTCCertificate",
		"RTCConfiguration", "RTCDataChannelStats",
		"RTCDegradationPreference", "RTCDtlsFingerprint", "RTCDtxStatus",
		"RTCEncodedAudioFrame", "RTCEncodedAudioFrameMetadata",
		"RTCEncodedFrameMetadata", "RTCEncodedVideoFrame",
		"RTCEncodedVideoFrameMetadata", "RTCEncodedVideoFrameType",
		"RTCInboundRtpStreamStats", "RTCLocalIceCandidateInit",
		"RTCOfferAnswerOptions", "RTCOfferOptions", "RTCOutboundRtpStreamStats",
		"RTCPeerConnectionState", "RTCPriorityType",
		"RTCReceivedRtpStreamStats", "RTCRemoteInboundRtpStreamStats",
		"RTCRemoteOutboundRtpStreamStats", "RTCRtcpMuxPolicy",
		"RTCSentRtpStreamStats", "RTCSignalingState",
		"RTCSetParameterOptions",
		"RTCTransportStats",
		"RTCPeerConnectionErrorCallback",
		"RTCSessionDescriptionCallback",
	}},
	{"web:web_codecs", "web/web_codecs.esc", []string{
		"AudioData", "AudioDataInit", "AudioDataCopyToOptions",
		"AudioSampleFormat",
		"AudioDecoder", "AudioDecoderConfig", "AudioDecoderInit",
		"AudioDecoderSupport",
		"AudioEncoder", "AudioEncoderConfig", "AudioEncoderInit",
		"AudioEncoderSupport",
		"VideoFrame", "VideoFrameInit", "VideoFrameBufferInit",
		"VideoFrameCopyToOptions", "VideoFrameMetadata",
		"VideoDecoder", "VideoDecoderConfig", "VideoDecoderInit",
		"VideoDecoderSupport",
		"VideoEncoder", "VideoEncoderConfig", "VideoEncoderInit",
		"VideoEncoderSupport", "VideoEncoderEncodeOptions",
		"VideoEncoderEncodeOptionsForAvc",
		"VideoColorPrimaries", "VideoColorSpace", "VideoColorSpaceInit",
		"VideoMatrixCoefficients", "VideoPixelFormat",
		"VideoTransferCharacteristics",
		"VideoEncoderBitrateMode",
		"EncodedAudioChunk", "EncodedAudioChunkInit",
		"EncodedAudioChunkMetadata", "EncodedAudioChunkType",
		"EncodedVideoChunk", "EncodedVideoChunkInit",
		"EncodedVideoChunkMetadata", "EncodedVideoChunkType",
		"ImageDecoder", "ImageDecoderInit", "ImageDecodeOptions",
		"ImageDecodeResult", "ImageTrack", "ImageTrackList",
		"WebCodecsErrorCallback",
		"PlaneLayout",
		"HardwareAcceleration",
		"AlphaOption", "LatencyMode", "AvcBitstreamFormat",
		"CodecState",
		"BitrateMode",
	}},
	{"web:indexeddb", "web/indexeddb.esc", []string{
		"IDBFactory", "IDBOpenDBRequest", "IDBOpenDBRequestEventMap",
		"IDBDatabase", "IDBDatabaseEventMap", "IDBDatabaseInfo",
		"IDBObjectStore", "IDBObjectStoreParameters",
		"IDBIndex", "IDBIndexParameters",
		"IDBCursor", "IDBCursorWithValue", "IDBCursorDirection",
		"IDBKeyRange",
		"IDBRequest", "IDBRequestEventMap", "IDBRequestReadyState",
		"IDBTransaction", "IDBTransactionEventMap", "IDBTransactionMode",
		"IDBTransactionOptions", "IDBTransactionDurability",
		"IDBVersionChangeEvent", "IDBVersionChangeEventInit",
		"IDBValidKey", "IDBArrayKey",
	}},
	{"web:service_worker", "web/service_worker.esc", []string{
		// Service Worker proper. MDN splits Push and Cache into their
		// own APIs (https://developer.mozilla.org/en-US/docs/Web/API/Service_Worker_API);
		// see web:push and web:cache below.
		//
		// Symbols that MDN documents under the Service Worker API but
		// that aren't present in the pinned lib.dom.d.ts (so they need
		// no partition entry here): Client, Clients, WindowClient,
		// ExtendableEvent, ExtendableMessageEvent, FetchEvent,
		// InstallEvent, ServiceWorkerGlobalScope. If a TS version bump
		// adds them, add the corresponding entries here.
		"ServiceWorker", "ServiceWorkerEventMap", "ServiceWorkerState",
		"ServiceWorkerContainer", "ServiceWorkerContainerEventMap",
		"ServiceWorkerRegistration", "ServiceWorkerRegistrationEventMap",
		"ServiceWorkerUpdateViaCache",
		"RegistrationOptions",
		"NavigationPreloadManager", "NavigationPreloadState",
		"FrameType", "ClientType",
	}},
	{"web:push", "web/push.esc", []string{
		// MDN documents Push as a separate API:
		// https://developer.mozilla.org/en-US/docs/Web/API/Push_API
		"PushManager", "PushSubscription", "PushSubscriptionJSON",
		"PushSubscriptionOptions", "PushSubscriptionOptionsInit",
		"PushEncryptionKeyName", "PushPermissionState",
	}},
	{"web:cache", "web/cache.esc", []string{
		// MDN documents the Cache API as a separate API (defined in the
		// SW spec but usable from any context):
		// https://developer.mozilla.org/en-US/docs/Web/API/Cache
		"CacheStorage", "Cache", "CacheQueryOptions",
		"MultiCacheQueryOptions",
	}},
	{"web:websocket", "web/websocket.esc", []string{
		"WebSocket", "WebSocketEventMap",
		"CloseEvent", "CloseEventInit",
	}},
	{"web:storage", "web/storage.esc", []string{
		"Storage", "StorageEvent", "StorageEventInit",
	}},
	{"web:url", "web/url.esc", []string{
		"URL", "URLSearchParams",
	}},
	{"web:encoding", "web/encoding.esc", []string{
		"TextEncoder", "TextEncoderCommon", "TextEncoderEncodeIntoResult",
		"TextDecoder", "TextDecoderCommon", "TextDecoderOptions",
		"TextDecodeOptions",
		// Per MDN, the *Stream variants belong to the Encoding API,
		// not the Streams API: https://developer.mozilla.org/en-US/docs/Web/API/Encoding_API
		"TextEncoderStream", "TextDecoderStream",
	}},
	{"web:file", "web/file.esc", []string{
		"Blob", "BlobPropertyBag", "BlobPart", "EndingType",
		"File", "FilePropertyBag",
		"FileList", "FileReader", "FileReaderEventMap",
	}},
	{"web:performance", "web/performance.esc", []string{
		// Symbols MDN documents under Performance that are absent from
		// the pinned lib.dom.d.ts (no partition entry needed today):
		// PerformanceEventTiming, PerformanceLongTaskTiming,
		// LargestContentfulPaint, LayoutShift, EventCounts,
		// TaskAttributionTiming, PerformanceElementTiming,
		// VisibilityStateEntry. Add here if a future TS version bump
		// ships them.
		"Performance", "PerformanceEventMap",
		"PerformanceEntry", "PerformanceEntryList",
		"PerformanceMark", "PerformanceMarkOptions",
		"PerformanceMeasure", "PerformanceMeasureOptions",
		"PerformanceNavigation", "PerformanceNavigationTiming",
		"PerformanceObserver", "PerformanceObserverCallback",
		"PerformanceObserverEntryList", "PerformanceObserverInit",
		"PerformancePaintTiming", "PerformanceResourceTiming",
		"PerformanceServerTiming", "PerformanceTiming",
		"performance",
	}},
	{"web:webauthn", "web/webauthn.esc", []string{
		"AuthenticatorAssertionResponse", "AuthenticatorAttestationResponse",
		"AuthenticatorResponse", "AuthenticatorTransport",
		"AuthenticatorAttachment", "AuthenticatorSelectionCriteria",
		"PublicKeyCredential", "PublicKeyCredentialCreationOptions",
		"PublicKeyCredentialRequestOptions",
		"PublicKeyCredentialDescriptor", "PublicKeyCredentialEntity",
		"PublicKeyCredentialParameters", "PublicKeyCredentialRpEntity",
		"PublicKeyCredentialType", "PublicKeyCredentialUserEntity",
		"PublicKeyCredentialJSON",
		"AttestationConveyancePreference",
		"UserVerificationRequirement", "ResidentKeyRequirement",
		"COSEAlgorithmIdentifier",
	}},
	{"web:payments", "web/payments.esc", []string{
		// Symbols MDN documents under the Payment Request API that
		// are absent from the pinned lib.dom.d.ts (no partition entry
		// needed today): PaymentAddress, PaymentRequestUpdateEvent,
		// MerchantValidationEvent, SecurePaymentConfirmationRequest.
		// Add here if a future TS version bump ships them.
		"PaymentRequest", "PaymentRequestEventMap",
		"PaymentResponse", "PaymentResponseEventMap",
		"PaymentMethodData", "PaymentMethodChangeEvent",
		"PaymentMethodChangeEventInit",
		"PaymentDetailsBase", "PaymentDetailsInit", "PaymentDetailsUpdate",
		"PaymentDetailsModifier",
		"PaymentItem", "PaymentShippingOption", "PaymentShippingType",
		"PaymentCurrencyAmount", "PaymentComplete",
		"PaymentValidationErrors",
		"AddressErrors", "PayerErrors",
		"ContactAddress",
	}},
}

// ExplicitDrops names top-level TS-lib declarations the converter
// skips emission for, with a logged note. Per §6.1: `globalThis` was
// the union of every previously-ambient name (now meaningless with
// no ambient tier), and `eval` has no good use case. Intrinsic-typed
// declarations (FR13) are detected structurally, not by name, and so
// are not listed here.
var ExplicitDrops = set.FromSlice([]string{
	// Per §6.1 — `globalThis` had no ambient union to take, `eval` has
	// no good use case.
	"globalThis",
	"eval",

	// FR13 intrinsics: checker-resident handlers with no source-level
	// declaration. The TS-lib file declares them as `type X<T> = intrinsic`
	// markers; the partitioner skips emission and the checker resolves
	// them directly.
	"Uppercase",
	"Lowercase",
	"Capitalize",
	"Uncapitalize",
	"NoInfer",

	// EvalError: per §FR1, dropped because `eval` is dropped — no
	// modern engine throws it from language semantics.
	"EvalError",
	"EvalErrorConstructor",

	// Legacy URI-encoding cousins. The §6.1 partition explicitly
	// enumerates encodeURI/decodeURI/encodeURIComponent/decodeURIComponent
	// in std:url and intentionally omits these two (deprecated since
	// ES1; superseded by encodeURIComponent).
	"escape",
	"unescape",

	// TS-side import-machinery types (`import.meta`, `import(...)`
	// option bags). These shape the TS module loader's surface, not
	// the language runtime; Escalier handles imports differently.
	"ImportMeta",
	"ImportAssertions",
	"ImportAttributes",
	"ImportCallOptions",
})

// DOMResidualSources is the set of `.d.ts` source-file basenames whose
// unmapped top-level declarations route to `web:dom` (the single-DOM
// package per §4.2). The catch-all exists because lib.dom.d.ts holds
// thousands of element classes, event types, and registry interfaces
// that all share one package; enumerating them in Partition would be
// pure noise that drifts on every TS version bump.
//
// Standalone web siblings (Fetch / Streams / Crypto / …) are mapped
// explicitly via webPackages above, so they take precedence over this
// residual rule even when they appear in lib.dom.d.ts.
var DOMResidualSources = set.FromSlice([]string{
	"lib.dom.d.ts",
	"lib.dom.iterable.d.ts",
	"lib.dom.asynciterable.d.ts",
})

func init() {
	for _, p := range stdPackages {
		pkg := Package{URI: p.URI, File: p.File}
		for _, m := range p.Members {
			if existing, ok := Partition[m]; ok {
				panic(fmt.Sprintf("partition: %q listed in both %s and %s",
					m, existing.URI, p.URI))
			}
			Partition[m] = pkg
		}
	}
	for _, p := range webPackages {
		pkg := Package{URI: p.URI, File: p.File}
		for _, m := range p.Members {
			if existing, ok := Partition[m]; ok {
				panic(fmt.Sprintf("partition: %q listed in both %s and %s",
					m, existing.URI, p.URI))
			}
			Partition[m] = pkg
		}
	}
}

// WebDOM is the catch-all package for lib.dom.d.ts symbols not pinned
// by the standalone-sibling explicit map. Returned by Route when the
// source file is in DOMResidualSources.
var WebDOM = Package{URI: "web:dom", File: "web/dom.esc"}

// RouteResult records the outcome of a Route call. Exactly one of Pkg,
// Drop, Unmapped is meaningful per result.
type RouteResult struct {
	// Pkg is set when the name routes to a known package (explicit map
	// or DOM residual).
	Pkg Package

	// Drop is true when the name is in ExplicitDrops. The caller should
	// skip emission and log.
	Drop bool

	// Unmapped is true when the name is neither in the partition, the
	// DOM-residual source list, nor the drop list. The caller fails per
	// §6.1's unmapped-symbol fail-safe.
	Unmapped bool
}

// Route returns the routing decision for a TS-lib top-level declaration
// named `name` that originated in `.d.ts` source file basename
// `sourceFile` (e.g. "lib.dom.d.ts", "lib.es5.d.ts"). Only the basename
// is consulted — the absolute path is not portable across `tsc` install
// layouts.
//
// Lookup order is as documented on Partition: explicit drops → explicit
// partition → DOM residual → unmapped fail-safe.
func Route(name, sourceFile string) RouteResult {
	if ExplicitDrops.Contains(name) {
		return RouteResult{Drop: true}
	}
	if pkg, ok := Partition[name]; ok {
		return RouteResult{Pkg: pkg}
	}
	// Constructor-suffix fallback: TS's class-via-trio idiom always
	// pairs `interface Foo` with `interface FooConstructor`. We keep
	// the explicit partition keyed on the instance name only — every
	// `XxxConstructor` follows its `Xxx` automatically so contributors
	// don't have to list both for each new partition entry.
	if stripped, ok := strings.CutSuffix(name, "Constructor"); ok && stripped != "" {
		if pkg, ok := Partition[stripped]; ok {
			return RouteResult{Pkg: pkg}
		}
	}
	if DOMResidualSources.Contains(sourceFile) {
		return RouteResult{Pkg: WebDOM}
	}
	return RouteResult{Unmapped: true}
}

// UnmappedError formats the fail-safe message per §6.1: name the
// symbol, its source `.d.ts` file, and point at the partition table
// file the contributor must edit.
func UnmappedError(name, sourceFile string) error {
	return fmt.Errorf(
		"converter: unmapped top-level declaration %q from %s; "+
			"add it to internal/interop/partition.go "+
			"(see planning/builtins/implementation_plan.md §6.1) "+
			"or to ExplicitDrops if intentional",
		name, sourceFile)
}

// PackageList returns the full list of known std/web package URIs in
// sorted order. Used by callers that need to enumerate the partition
// (e.g. the §6.3 output-layout step that creates an empty file per
// package even if no symbol routed there in this pass).
func PackageList() []string {
	uris := make([]string, 0, len(stdPackages)+len(webPackages)+1)
	for _, p := range stdPackages {
		uris = append(uris, p.URI)
	}
	for _, p := range webPackages {
		uris = append(uris, p.URI)
	}
	uris = append(uris, WebDOM.URI)
	sort.Strings(uris)
	return uris
}

// PackageForURI returns the Package for a known URI, or false if the
// URI is not in the std/web partition. `web:dom` is included.
func PackageForURI(uri string) (Package, bool) {
	for _, p := range stdPackages {
		if p.URI == uri {
			return Package{URI: p.URI, File: p.File}, true
		}
	}
	for _, p := range webPackages {
		if p.URI == uri {
			return Package{URI: p.URI, File: p.File}, true
		}
	}
	if uri == WebDOM.URI {
		return WebDOM, true
	}
	return Package{}, false
}

// SchemeOf returns the URI scheme prefix (the segment before the first
// colon) for an import-style URI, or "" if there is no colon. Helper
// for callers that need to bucket package URIs by realm.
func SchemeOf(uri string) string {
	scheme, _, ok := strings.Cut(uri, ":")
	if !ok {
		return ""
	}
	return scheme
}
