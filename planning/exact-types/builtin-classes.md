# Classes in TypeScript lib files (Escalier interpretation)

Total: 692 classes

## Hierarchy (roots and their subclasses)

- AbstractRange
  - Range
  - StaticRange
- AnimationEffect
  - KeyframeEffect
- AnimationTimeline
  - DocumentTimeline
- AuthenticatorResponse
  - AuthenticatorAssertionResponse
  - AuthenticatorAttestationResponse
- Blob
  - File
- CSSRule
  - CSSCounterStyleRule
  - CSSFontFaceRule
  - CSSFontFeatureValuesRule
  - CSSFontPaletteValuesRule
  - CSSGroupingRule
    - CSSConditionRule
      - CSSContainerRule
      - CSSMediaRule
      - CSSSupportsRule
    - CSSLayerBlockRule
    - CSSPageRule
    - CSSScopeRule
    - CSSStartingStyleRule
    - CSSStyleRule
  - CSSImportRule
  - CSSKeyframeRule
  - CSSKeyframesRule
  - CSSLayerStatementRule
  - CSSNamespaceRule
  - CSSNestedDeclarations
  - CSSPropertyRule
  - CSSViewTransitionRule
- CSSStyleValue
  - CSSImageValue
  - CSSKeywordValue
  - CSSNumericValue
    - CSSMathValue
      - CSSMathClamp
      - CSSMathInvert
      - CSSMathMax
      - CSSMathMin
      - CSSMathNegate
      - CSSMathProduct
      - CSSMathSum
    - CSSUnitValue
  - CSSTransformValue
  - CSSUnparsedValue
- CSSTransformComponent
  - CSSMatrixComponent
  - CSSPerspective
  - CSSRotate
  - CSSScale
  - CSSSkew
  - CSSSkewX
  - CSSSkewY
  - CSSTranslate
- Credential
  - PublicKeyCredential
- DOMMatrixReadOnly
  - DOMMatrix
- DOMPointReadOnly
  - DOMPoint
- DOMRectReadOnly
  - DOMRect
- Error
  - AggregateError
  - CompileError
  - DOMException
    - OverconstrainedError
    - RTCError
    - WebTransportError
  - EvalError
  - LinkError
  - RangeError
  - ReferenceError
  - RuntimeError
  - SuppressedError
  - SyntaxError
  - TypeError
  - URIError
- Event
  - AnimationEvent
  - AnimationPlaybackEvent
  - AudioProcessingEvent
  - BeforeUnloadEvent
  - BlobEvent
  - ClipboardEvent
  - CloseEvent
  - ContentVisibilityAutoStateChangeEvent
  - CustomEvent
  - DeviceMotionEvent
  - DeviceOrientationEvent
  - ErrorEvent
  - FontFaceSetLoadEvent
  - FormDataEvent
  - GamepadEvent
  - HashChangeEvent
  - IDBVersionChangeEvent
  - MIDIConnectionEvent
  - MIDIMessageEvent
  - MediaEncryptedEvent
  - MediaKeyMessageEvent
  - MediaQueryListEvent
  - MediaStreamTrackEvent
  - MessageEvent
  - OfflineAudioCompletionEvent
  - PageRevealEvent
  - PageSwapEvent
  - PageTransitionEvent
  - PaymentRequestUpdateEvent
    - PaymentMethodChangeEvent
  - PictureInPictureEvent
  - PopStateEvent
  - ProgressEvent
  - PromiseRejectionEvent
  - RTCDTMFToneChangeEvent
  - RTCDataChannelEvent
  - RTCErrorEvent
  - RTCPeerConnectionIceErrorEvent
  - RTCPeerConnectionIceEvent
  - RTCTrackEvent
  - SecurityPolicyViolationEvent
  - SpeechSynthesisEvent
    - SpeechSynthesisErrorEvent
  - StorageEvent
  - SubmitEvent
  - ToggleEvent
  - TrackEvent
  - TransitionEvent
  - UIEvent
    - CompositionEvent
    - FocusEvent
    - InputEvent
    - KeyboardEvent
    - MouseEvent
      - DragEvent
      - PointerEvent
      - WheelEvent
    - TextEvent
    - TouchEvent
  - WebGLContextEvent
- EventTarget
  - AbortSignal
  - Animation
    - CSSAnimation
    - CSSTransition
  - AudioDecoder
  - AudioEncoder
  - AudioNode
    - AnalyserNode
    - AudioDestinationNode
    - AudioScheduledSourceNode
      - AudioBufferSourceNode
      - ConstantSourceNode
      - OscillatorNode
    - AudioWorkletNode
    - BiquadFilterNode
    - ChannelMergerNode
    - ChannelSplitterNode
    - ConvolverNode
    - DelayNode
    - DynamicsCompressorNode
    - GainNode
    - IIRFilterNode
    - MediaElementAudioSourceNode
    - MediaStreamAudioDestinationNode
    - MediaStreamAudioSourceNode
    - PannerNode
    - ScriptProcessorNode
    - StereoPannerNode
    - WaveShaperNode
  - BaseAudioContext
    - AudioContext
    - OfflineAudioContext
  - BroadcastChannel
  - Clipboard
  - EventSource
  - FileReader
  - FontFaceSet
  - IDBDatabase
  - IDBRequest
    - IDBOpenDBRequest
  - IDBTransaction
  - MIDIAccess
  - MIDIPort
    - MIDIInput
    - MIDIOutput
  - MediaDevices
  - MediaKeySession
  - MediaQueryList
  - MediaRecorder
  - MediaSource
  - MediaStream
  - MediaStreamTrack
    - CanvasCaptureMediaStreamTrack
  - MessagePort
  - NavigationHistoryEntry
  - Node
    - Attr
    - CharacterData
      - Comment
      - ProcessingInstruction
      - Text
        - CDATASection
    - Document
      - HTMLDocument
      - XMLDocument
    - DocumentFragment
      - ShadowRoot
    - DocumentType
    - Element
      - HTMLElement
        - HTMLAnchorElement
        - HTMLAreaElement
        - HTMLBRElement
        - HTMLBaseElement
        - HTMLBodyElement
        - HTMLButtonElement
        - HTMLCanvasElement
        - HTMLDListElement
        - HTMLDataElement
        - HTMLDataListElement
        - HTMLDetailsElement
        - HTMLDialogElement
        - HTMLDirectoryElement
        - HTMLDivElement
        - HTMLEmbedElement
        - HTMLFieldSetElement
        - HTMLFontElement
        - HTMLFormElement
        - HTMLFrameElement
        - HTMLFrameSetElement
        - HTMLHRElement
        - HTMLHeadElement
        - HTMLHeadingElement
        - HTMLHtmlElement
        - HTMLIFrameElement
        - HTMLImageElement
        - HTMLInputElement
        - HTMLLIElement
        - HTMLLabelElement
        - HTMLLegendElement
        - HTMLLinkElement
        - HTMLMapElement
        - HTMLMarqueeElement
        - HTMLMediaElement
          - HTMLAudioElement
          - HTMLVideoElement
        - HTMLMenuElement
        - HTMLMetaElement
        - HTMLMeterElement
        - HTMLModElement
        - HTMLOListElement
        - HTMLObjectElement
        - HTMLOptGroupElement
        - HTMLOptionElement
        - HTMLOutputElement
        - HTMLParagraphElement
        - HTMLParamElement
        - HTMLPictureElement
        - HTMLPreElement
        - HTMLProgressElement
        - HTMLQuoteElement
        - HTMLScriptElement
        - HTMLSelectElement
        - HTMLSlotElement
        - HTMLSourceElement
        - HTMLSpanElement
        - HTMLStyleElement
        - HTMLTableCaptionElement
        - HTMLTableCellElement
        - HTMLTableColElement
        - HTMLTableElement
        - HTMLTableRowElement
        - HTMLTableSectionElement
        - HTMLTemplateElement
        - HTMLTextAreaElement
        - HTMLTimeElement
        - HTMLTitleElement
        - HTMLTrackElement
        - HTMLUListElement
        - HTMLUnknownElement
      - MathMLElement
      - SVGElement
        - SVGAnimationElement
          - SVGAnimateElement
          - SVGAnimateMotionElement
          - SVGAnimateTransformElement
          - SVGSetElement
        - SVGClipPathElement
        - SVGComponentTransferFunctionElement
          - SVGFEFuncAElement
          - SVGFEFuncBElement
          - SVGFEFuncGElement
          - SVGFEFuncRElement
        - SVGDescElement
        - SVGFEBlendElement
        - SVGFEColorMatrixElement
        - SVGFEComponentTransferElement
        - SVGFECompositeElement
        - SVGFEConvolveMatrixElement
        - SVGFEDiffuseLightingElement
        - SVGFEDisplacementMapElement
        - SVGFEDistantLightElement
        - SVGFEDropShadowElement
        - SVGFEFloodElement
        - SVGFEGaussianBlurElement
        - SVGFEImageElement
        - SVGFEMergeElement
        - SVGFEMergeNodeElement
        - SVGFEMorphologyElement
        - SVGFEOffsetElement
        - SVGFEPointLightElement
        - SVGFESpecularLightingElement
        - SVGFESpotLightElement
        - SVGFETileElement
        - SVGFETurbulenceElement
        - SVGFilterElement
        - SVGGradientElement
          - SVGLinearGradientElement
          - SVGRadialGradientElement
        - SVGGraphicsElement
          - SVGAElement
          - SVGDefsElement
          - SVGForeignObjectElement
          - SVGGElement
          - SVGGeometryElement
            - SVGCircleElement
            - SVGEllipseElement
            - SVGLineElement
            - SVGPathElement
            - SVGPolygonElement
            - SVGPolylineElement
            - SVGRectElement
          - SVGImageElement
          - SVGSVGElement
          - SVGSwitchElement
          - SVGTextContentElement
            - SVGTextPathElement
            - SVGTextPositioningElement
              - SVGTSpanElement
              - SVGTextElement
          - SVGUseElement
        - SVGMPathElement
        - SVGMarkerElement
        - SVGMaskElement
        - SVGMetadataElement
        - SVGPatternElement
        - SVGScriptElement
        - SVGStopElement
        - SVGStyleElement
        - SVGSymbolElement
        - SVGTitleElement
        - SVGViewElement
  - Notification
  - OffscreenCanvas
  - PaymentRequest
  - PaymentResponse
  - Performance
  - PermissionStatus
  - PictureInPictureWindow
  - RTCDTMFSender
  - RTCDataChannel
  - RTCDtlsTransport
  - RTCIceTransport
  - RTCPeerConnection
  - RTCSctpTransport
  - RemotePlayback
  - ScreenOrientation
  - ServiceWorker
  - ServiceWorkerContainer
  - ServiceWorkerRegistration
  - SharedWorker
  - SourceBuffer
  - SourceBufferList
  - SpeechSynthesis
  - SpeechSynthesisUtterance
  - TextTrack
  - TextTrackCue
    - VTTCue
  - TextTrackList
  - VideoDecoder
  - VideoEncoder
  - VisualViewport
  - WakeLockSentinel
  - WebSocket
  - Window
  - Worker
  - XMLHttpRequestEventTarget
    - XMLHttpRequest
    - XMLHttpRequestUpload
- FileSystemEntry
  - FileSystemDirectoryEntry
  - FileSystemFileEntry
- FileSystemHandle
  - FileSystemDirectoryHandle
  - FileSystemFileHandle
- IDBCursor
  - IDBCursorWithValue
- Map
  - HighlightRegistry
- MediaDeviceInfo
  - InputDeviceInfo
- NodeList
  - RadioNodeList
- PerformanceEntry
  - LargestContentfulPaint
  - PerformanceEventTiming
  - PerformanceMark
  - PerformanceMeasure
  - PerformancePaintTiming
  - PerformanceResourceTiming
    - PerformanceNavigationTiming
- Set
  - CustomStateSet
  - FontFaceSet
  - Highlight
  - ViewTransitionTypeSet
- StylePropertyMapReadOnly
  - StylePropertyMap
- StyleSheet
  - CSSStyleSheet
- Worklet
  - AudioWorklet
- WritableStream
  - FileSystemWritableFileStream

## Standalone classes (no subclasses in lib)

- AbortController
- Array
- ArrayBuffer
- AsyncDisposableStack
- AudioBuffer
- AudioData
- AudioListener
- AudioParam
- AudioParamMap
- BarProp
- BigInt
- BigInt64Array
- BigUint64Array
- Boolean
- ByteLengthQueuingStrategy
- CSSNumericArray
- CSSRuleList
- CSSStyleDeclaration
- CSSVariableReferenceValue
- Cache
- CacheStorage
- CanvasGradient
- CanvasPattern
- CanvasRenderingContext2D
- CaretPosition
- ClipboardItem
- Collator
- CompressionStream
- CountQueuingStrategy
- CredentialsContainer
- Crypto
- CryptoKey
- CustomElementRegistry
- DOMImplementation
- DOMParser
- DOMQuad
- DOMRectList
- DOMStringList
- DOMStringMap
- DOMTokenList
- DataTransfer
- DataTransferItem
- DataTransferItemList
- DataView
- Date
- DateTimeFormat
- DecompressionStream
- DisplayNames
- DisposableStack
- ElementInternals
- EncodedAudioChunk
- EncodedVideoChunk
- Enumerator
- EventCounts
- External
- FileList
- FileSystem
- FileSystemDirectoryReader
- FinalizationRegistry
- Float16Array
- Float32Array
- Float64Array
- FontFace
- FormData
- FragmentDirective
- Function
- Gamepad
- GamepadButton
- GamepadHapticActuator
- Geolocation
- GeolocationCoordinates
- GeolocationPosition
- GeolocationPositionError
- Global
- HTMLAllCollection
- HTMLCollection
- HTMLFormControlsCollection
- HTMLOptionsCollection
- Headers
- History
- IDBFactory
- IDBIndex
- IDBKeyRange
- IDBObjectStore
- IdleDeadline
- ImageBitmap
- ImageBitmapRenderingContext
- ImageData
- ImageDecoder
- ImageTrack
- ImageTrackList
- Instance
- Int16Array
- Int32Array
- Int8Array
- IntersectionObserver
- IntersectionObserverEntry
- Iterator
- ListFormat
- Location
- Lock
- LockManager
- MIDIInputMap
- MIDIOutputMap
- MediaCapabilities
- MediaError
- MediaKeyStatusMap
- MediaKeySystemAccess
- MediaKeys
- MediaList
- MediaMetadata
- MediaSession
- MediaSourceHandle
- Memory
- MessageChannel
- MimeType
- MimeTypeArray
- Module
- MutationObserver
- MutationRecord
- NamedNodeMap
- NavigationActivation
- NavigationPreloadManager
- Navigator
- NodeIterator
- Number
- NumberFormat
- Object
- OffscreenCanvasRenderingContext2D
- Path2D
- PaymentAddress
- PerformanceNavigation
- PerformanceObserver
- PerformanceObserverEntryList
- PerformanceServerTiming
- PerformanceTiming
- PeriodicWave
- Permissions
- Plugin
- PluginArray
- PluralRules
- Promise
- PushManager
- PushSubscription
- PushSubscriptionOptions
- RTCCertificate
- RTCEncodedAudioFrame
- RTCEncodedVideoFrame
- RTCIceCandidate
- RTCRtpReceiver
- RTCRtpScriptTransform
- RTCRtpSender
- RTCRtpTransceiver
- RTCSessionDescription
- RTCStatsReport
- ReadableByteStreamController
- ReadableStream
- ReadableStreamBYOBReader
- ReadableStreamBYOBRequest
- ReadableStreamDefaultController
- ReadableStreamDefaultReader
- RegExp
- Report
- ReportBody
- ReportingObserver
- Request
- ResizeObserver
- ResizeObserverEntry
- ResizeObserverSize
- Response
- SVGAngle
- SVGAnimatedAngle
- SVGAnimatedBoolean
- SVGAnimatedEnumeration
- SVGAnimatedInteger
- SVGAnimatedLength
- SVGAnimatedLengthList
- SVGAnimatedNumber
- SVGAnimatedNumberList
- SVGAnimatedPreserveAspectRatio
- SVGAnimatedRect
- SVGAnimatedString
- SVGAnimatedTransformList
- SVGLength
- SVGLengthList
- SVGNumber
- SVGNumberList
- SVGPointList
- SVGPreserveAspectRatio
- SVGStringList
- SVGTransform
- SVGTransformList
- SVGUnitTypes
- Screen
- Segmenter
- Selection
- SharedArrayBuffer
- SpeechRecognitionAlternative
- SpeechRecognitionResult
- SpeechRecognitionResultList
- SpeechSynthesisVoice
- Storage
- StorageManager
- String
- StyleSheetList
- SubtleCrypto
- Symbol
- Table
- TextDecoder
- TextDecoderStream
- TextEncoder
- TextEncoderStream
- TextMetrics
- TextTrackCueList
- TimeRanges
- Touch
- TouchList
- TransformStream
- TransformStreamDefaultController
- TreeWalker
- URL
- URLSearchParams
- Uint16Array
- Uint32Array
- Uint8Array
- Uint8ClampedArray
- UserActivation
- VBArray
- VTTRegion
- ValidityState
- VideoColorSpace
- VideoFrame
- VideoPlaybackQuality
- ViewTransition
- WakeLock
- WeakMap
- WeakRef
- WeakSet
- WebGL2RenderingContext
- WebGLActiveInfo
- WebGLBuffer
- WebGLFramebuffer
- WebGLProgram
- WebGLQuery
- WebGLRenderbuffer
- WebGLRenderingContext
- WebGLSampler
- WebGLShader
- WebGLShaderPrecisionFormat
- WebGLSync
- WebGLTexture
- WebGLTransformFeedback
- WebGLUniformLocation
- WebGLVertexArrayObject
- WebTransport
- WebTransportBidirectionalStream
- WebTransportDatagramDuplexStream
- WritableStreamDefaultController
- WritableStreamDefaultWriter
- XMLSerializer
- XPathEvaluator
- XPathExpression
- XPathResult
- XSLTProcessor

## All classes (alphabetical) with parents and source files

- **AbortController** (parents: —)
    files: lib.dom.d.ts
- **AbortSignal** (parents: EventTarget)
    files: lib.dom.d.ts
- **AbstractRange** (parents: —)
    files: lib.dom.d.ts
- **AggregateError** (parents: Error)
    files: lib.es2021.promise.d.ts
- **AnalyserNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **Animation** (parents: EventTarget)
    files: lib.dom.d.ts
- **AnimationEffect** (parents: —)
    files: lib.dom.d.ts
- **AnimationEvent** (parents: Event)
    files: lib.dom.d.ts
- **AnimationPlaybackEvent** (parents: Event)
    files: lib.dom.d.ts
- **AnimationTimeline** (parents: —)
    files: lib.dom.d.ts
- **Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2019.array.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **ArrayBuffer** (parents: —)
    files: lib.es2015.symbol.wellknown.d.ts, lib.es2024.arraybuffer.d.ts, lib.es5.d.ts
- **AsyncDisposableStack** (parents: —)
    files: lib.esnext.disposable.d.ts
- **Attr** (parents: Node)
    files: lib.dom.d.ts
- **AudioBuffer** (parents: —)
    files: lib.dom.d.ts
- **AudioBufferSourceNode** (parents: AudioScheduledSourceNode)
    files: lib.dom.d.ts
- **AudioContext** (parents: BaseAudioContext)
    files: lib.dom.d.ts
- **AudioData** (parents: —)
    files: lib.dom.d.ts
- **AudioDecoder** (parents: EventTarget)
    files: lib.dom.d.ts
- **AudioDestinationNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **AudioEncoder** (parents: EventTarget)
    files: lib.dom.d.ts
- **AudioListener** (parents: —)
    files: lib.dom.d.ts
- **AudioNode** (parents: EventTarget)
    files: lib.dom.d.ts
- **AudioParam** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **AudioParamMap** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **AudioProcessingEvent** (parents: Event)
    files: lib.dom.d.ts
- **AudioScheduledSourceNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **AudioWorklet** (parents: Worklet)
    files: lib.dom.d.ts
- **AudioWorkletNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **AuthenticatorAssertionResponse** (parents: AuthenticatorResponse)
    files: lib.dom.d.ts
- **AuthenticatorAttestationResponse** (parents: AuthenticatorResponse)
    files: lib.dom.d.ts
- **AuthenticatorResponse** (parents: —)
    files: lib.dom.d.ts
- **BarProp** (parents: —)
    files: lib.dom.d.ts
- **BaseAudioContext** (parents: EventTarget)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **BeforeUnloadEvent** (parents: Event)
    files: lib.dom.d.ts
- **BigInt** (parents: —)
    files: lib.es2020.bigint.d.ts
- **BigInt64Array** (parents: —)
    files: lib.es2020.bigint.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts
- **BigUint64Array** (parents: —)
    files: lib.es2020.bigint.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts
- **BiquadFilterNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **Blob** (parents: —)
    files: lib.dom.d.ts
- **BlobEvent** (parents: Event)
    files: lib.dom.d.ts
- **Boolean** (parents: —)
    files: lib.es5.d.ts
- **BroadcastChannel** (parents: EventTarget)
    files: lib.dom.d.ts
- **ByteLengthQueuingStrategy** (parents: —)
    files: lib.dom.d.ts
- **CDATASection** (parents: Text)
    files: lib.dom.d.ts
- **CSSAnimation** (parents: Animation)
    files: lib.dom.d.ts
- **CSSConditionRule** (parents: CSSGroupingRule)
    files: lib.dom.d.ts
- **CSSContainerRule** (parents: CSSConditionRule)
    files: lib.dom.d.ts
- **CSSCounterStyleRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSFontFaceRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSFontFeatureValuesRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSFontPaletteValuesRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSGroupingRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSImageValue** (parents: CSSStyleValue)
    files: lib.dom.d.ts
- **CSSImportRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSKeyframeRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSKeyframesRule** (parents: CSSRule)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CSSKeywordValue** (parents: CSSStyleValue)
    files: lib.dom.d.ts
- **CSSLayerBlockRule** (parents: CSSGroupingRule)
    files: lib.dom.d.ts
- **CSSLayerStatementRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSMathClamp** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathInvert** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathMax** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathMin** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathNegate** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathProduct** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathSum** (parents: CSSMathValue)
    files: lib.dom.d.ts
- **CSSMathValue** (parents: CSSNumericValue)
    files: lib.dom.d.ts
- **CSSMatrixComponent** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSMediaRule** (parents: CSSConditionRule)
    files: lib.dom.d.ts
- **CSSNamespaceRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSNestedDeclarations** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSNumericArray** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CSSNumericValue** (parents: CSSStyleValue)
    files: lib.dom.d.ts
- **CSSPageRule** (parents: CSSGroupingRule)
    files: lib.dom.d.ts
- **CSSPerspective** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSPropertyRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **CSSRotate** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSRule** (parents: —)
    files: lib.dom.d.ts
- **CSSRuleList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CSSScale** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSScopeRule** (parents: CSSGroupingRule)
    files: lib.dom.d.ts
- **CSSSkew** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSSkewX** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSSkewY** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSStartingStyleRule** (parents: CSSGroupingRule)
    files: lib.dom.d.ts
- **CSSStyleDeclaration** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CSSStyleRule** (parents: CSSGroupingRule)
    files: lib.dom.d.ts
- **CSSStyleSheet** (parents: StyleSheet)
    files: lib.dom.d.ts
- **CSSStyleValue** (parents: —)
    files: lib.dom.d.ts
- **CSSSupportsRule** (parents: CSSConditionRule)
    files: lib.dom.d.ts
- **CSSTransformComponent** (parents: —)
    files: lib.dom.d.ts
- **CSSTransformValue** (parents: CSSStyleValue)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CSSTransition** (parents: Animation)
    files: lib.dom.d.ts
- **CSSTranslate** (parents: CSSTransformComponent)
    files: lib.dom.d.ts
- **CSSUnitValue** (parents: CSSNumericValue)
    files: lib.dom.d.ts
- **CSSUnparsedValue** (parents: CSSStyleValue)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CSSVariableReferenceValue** (parents: —)
    files: lib.dom.d.ts
- **CSSViewTransitionRule** (parents: CSSRule)
    files: lib.dom.d.ts
- **Cache** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **CacheStorage** (parents: —)
    files: lib.dom.d.ts
- **CanvasCaptureMediaStreamTrack** (parents: MediaStreamTrack)
    files: lib.dom.d.ts
- **CanvasGradient** (parents: —)
    files: lib.dom.d.ts
- **CanvasPattern** (parents: —)
    files: lib.dom.d.ts
- **CanvasRenderingContext2D** (parents: —)
    files: lib.dom.d.ts
- **CaretPosition** (parents: —)
    files: lib.dom.d.ts
- **ChannelMergerNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **ChannelSplitterNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **CharacterData** (parents: Node)
    files: lib.dom.d.ts
- **Clipboard** (parents: EventTarget)
    files: lib.dom.d.ts
- **ClipboardEvent** (parents: Event)
    files: lib.dom.d.ts
- **ClipboardItem** (parents: —)
    files: lib.dom.d.ts
- **CloseEvent** (parents: Event)
    files: lib.dom.d.ts
- **Collator** (parents: —)
    files: lib.es5.d.ts
- **Comment** (parents: CharacterData)
    files: lib.dom.d.ts
- **CompileError** (parents: Error)
    files: lib.dom.d.ts
- **CompositionEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **CompressionStream** (parents: —)
    files: lib.dom.d.ts
- **ConstantSourceNode** (parents: AudioScheduledSourceNode)
    files: lib.dom.d.ts
- **ContentVisibilityAutoStateChangeEvent** (parents: Event)
    files: lib.dom.d.ts
- **ConvolverNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **CountQueuingStrategy** (parents: —)
    files: lib.dom.d.ts
- **Credential** (parents: —)
    files: lib.dom.d.ts
- **CredentialsContainer** (parents: —)
    files: lib.dom.d.ts
- **Crypto** (parents: —)
    files: lib.dom.d.ts
- **CryptoKey** (parents: —)
    files: lib.dom.d.ts
- **CustomElementRegistry** (parents: —)
    files: lib.dom.d.ts
- **CustomEvent** (parents: Event)
    files: lib.dom.d.ts
- **CustomStateSet** (parents: Set)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **DOMException** (parents: Error)
    files: lib.dom.d.ts
- **DOMImplementation** (parents: —)
    files: lib.dom.d.ts
- **DOMMatrix** (parents: DOMMatrixReadOnly)
    files: lib.dom.d.ts
- **DOMMatrixReadOnly** (parents: —)
    files: lib.dom.d.ts
- **DOMParser** (parents: —)
    files: lib.dom.d.ts
- **DOMPoint** (parents: DOMPointReadOnly)
    files: lib.dom.d.ts
- **DOMPointReadOnly** (parents: —)
    files: lib.dom.d.ts
- **DOMQuad** (parents: —)
    files: lib.dom.d.ts
- **DOMRect** (parents: DOMRectReadOnly)
    files: lib.dom.d.ts
- **DOMRectList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **DOMRectReadOnly** (parents: —)
    files: lib.dom.d.ts
- **DOMStringList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **DOMStringMap** (parents: —)
    files: lib.dom.d.ts
- **DOMTokenList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **DataTransfer** (parents: —)
    files: lib.dom.d.ts
- **DataTransferItem** (parents: —)
    files: lib.dom.d.ts
- **DataTransferItemList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **DataView** (parents: —)
    files: lib.es2015.symbol.wellknown.d.ts, lib.es2020.bigint.d.ts, lib.es5.d.ts, lib.esnext.float16.d.ts
- **Date** (parents: —)
    files: lib.es2015.symbol.wellknown.d.ts, lib.es2020.date.d.ts, lib.es5.d.ts, lib.scripthost.d.ts
- **DateTimeFormat** (parents: —)
    files: lib.es2017.intl.d.ts, lib.es2021.intl.d.ts, lib.es5.d.ts
- **DecompressionStream** (parents: —)
    files: lib.dom.d.ts
- **DelayNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **DeviceMotionEvent** (parents: Event)
    files: lib.dom.d.ts
- **DeviceOrientationEvent** (parents: Event)
    files: lib.dom.d.ts
- **DisplayNames** (parents: —)
    files: lib.es2020.intl.d.ts
- **DisposableStack** (parents: —)
    files: lib.esnext.disposable.d.ts
- **Document** (parents: Node)
    files: lib.dom.d.ts
- **DocumentFragment** (parents: Node)
    files: lib.dom.d.ts
- **DocumentTimeline** (parents: AnimationTimeline)
    files: lib.dom.d.ts
- **DocumentType** (parents: Node)
    files: lib.dom.d.ts
- **DragEvent** (parents: MouseEvent)
    files: lib.dom.d.ts
- **DynamicsCompressorNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **Element** (parents: Node)
    files: lib.dom.d.ts
- **ElementInternals** (parents: —)
    files: lib.dom.d.ts
- **EncodedAudioChunk** (parents: —)
    files: lib.dom.d.ts
- **EncodedVideoChunk** (parents: —)
    files: lib.dom.d.ts
- **Enumerator** (parents: —)
    files: lib.scripthost.d.ts
- **Error** (parents: —)
    files: lib.es2022.error.d.ts, lib.es5.d.ts
- **ErrorEvent** (parents: Event)
    files: lib.dom.d.ts
- **EvalError** (parents: Error)
    files: lib.es5.d.ts
- **Event** (parents: —)
    files: lib.dom.d.ts
- **EventCounts** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **EventSource** (parents: EventTarget)
    files: lib.dom.d.ts
- **EventTarget** (parents: —)
    files: lib.dom.d.ts
- **External** (parents: —)
    files: lib.dom.d.ts
- **File** (parents: Blob)
    files: lib.dom.d.ts
- **FileList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **FileReader** (parents: EventTarget)
    files: lib.dom.d.ts
- **FileSystem** (parents: —)
    files: lib.dom.d.ts
- **FileSystemDirectoryEntry** (parents: FileSystemEntry)
    files: lib.dom.d.ts
- **FileSystemDirectoryHandle** (parents: FileSystemHandle)
    files: lib.dom.asynciterable.d.ts, lib.dom.d.ts
- **FileSystemDirectoryReader** (parents: —)
    files: lib.dom.d.ts
- **FileSystemEntry** (parents: —)
    files: lib.dom.d.ts
- **FileSystemFileEntry** (parents: FileSystemEntry)
    files: lib.dom.d.ts
- **FileSystemFileHandle** (parents: FileSystemHandle)
    files: lib.dom.d.ts
- **FileSystemHandle** (parents: —)
    files: lib.dom.d.ts
- **FileSystemWritableFileStream** (parents: WritableStream)
    files: lib.dom.d.ts
- **FinalizationRegistry** (parents: —)
    files: lib.es2021.weakref.d.ts
- **Float16Array** (parents: —)
    files: lib.esnext.float16.d.ts
- **Float32Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **Float64Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **FocusEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **FontFace** (parents: —)
    files: lib.dom.d.ts
- **FontFaceSet** (parents: EventTarget, Set)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **FontFaceSetLoadEvent** (parents: Event)
    files: lib.dom.d.ts
- **FormData** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **FormDataEvent** (parents: Event)
    files: lib.dom.d.ts
- **FragmentDirective** (parents: —)
    files: lib.dom.d.ts
- **Function** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es5.d.ts, lib.esnext.decorators.d.ts
- **GainNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **Gamepad** (parents: —)
    files: lib.dom.d.ts
- **GamepadButton** (parents: —)
    files: lib.dom.d.ts
- **GamepadEvent** (parents: Event)
    files: lib.dom.d.ts
- **GamepadHapticActuator** (parents: —)
    files: lib.dom.d.ts
- **Geolocation** (parents: —)
    files: lib.dom.d.ts
- **GeolocationCoordinates** (parents: —)
    files: lib.dom.d.ts
- **GeolocationPosition** (parents: —)
    files: lib.dom.d.ts
- **GeolocationPositionError** (parents: —)
    files: lib.dom.d.ts
- **Global** (parents: —)
    files: lib.dom.d.ts
- **HTMLAllCollection** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **HTMLAnchorElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLAreaElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLAudioElement** (parents: HTMLMediaElement)
    files: lib.dom.d.ts
- **HTMLBRElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLBaseElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLBodyElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLButtonElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLCanvasElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLCollection** (parents: —)
    files: lib.dom.d.ts
- **HTMLDListElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDataElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDataListElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDetailsElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDialogElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDirectoryElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDivElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLDocument** (parents: Document)
    files: lib.dom.d.ts
- **HTMLElement** (parents: Element)
    files: lib.dom.d.ts
- **HTMLEmbedElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLFieldSetElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLFontElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLFormControlsCollection** (parents: —)
    files: lib.dom.d.ts
- **HTMLFormElement** (parents: HTMLElement)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **HTMLFrameElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLFrameSetElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLHRElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLHeadElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLHeadingElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLHtmlElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLIFrameElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLImageElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLInputElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLLIElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLLabelElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLLegendElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLLinkElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLMapElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLMarqueeElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLMediaElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLMenuElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLMetaElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLMeterElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLModElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLOListElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLObjectElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLOptGroupElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLOptionElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLOptionsCollection** (parents: —)
    files: lib.dom.d.ts
- **HTMLOutputElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLParagraphElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLParamElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLPictureElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLPreElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLProgressElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLQuoteElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLScriptElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLSelectElement** (parents: HTMLElement)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **HTMLSlotElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLSourceElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLSpanElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLStyleElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTableCaptionElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTableCellElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTableColElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTableElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTableRowElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTableSectionElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTemplateElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTextAreaElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTimeElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTitleElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLTrackElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLUListElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLUnknownElement** (parents: HTMLElement)
    files: lib.dom.d.ts
- **HTMLVideoElement** (parents: HTMLMediaElement)
    files: lib.dom.d.ts
- **HashChangeEvent** (parents: Event)
    files: lib.dom.d.ts
- **Headers** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **Highlight** (parents: Set)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **HighlightRegistry** (parents: Map)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **History** (parents: —)
    files: lib.dom.d.ts
- **IDBCursor** (parents: —)
    files: lib.dom.d.ts
- **IDBCursorWithValue** (parents: IDBCursor)
    files: lib.dom.d.ts
- **IDBDatabase** (parents: EventTarget)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **IDBFactory** (parents: —)
    files: lib.dom.d.ts
- **IDBIndex** (parents: —)
    files: lib.dom.d.ts
- **IDBKeyRange** (parents: —)
    files: lib.dom.d.ts
- **IDBObjectStore** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **IDBOpenDBRequest** (parents: IDBRequest)
    files: lib.dom.d.ts
- **IDBRequest** (parents: EventTarget)
    files: lib.dom.d.ts
- **IDBTransaction** (parents: EventTarget)
    files: lib.dom.d.ts
- **IDBVersionChangeEvent** (parents: Event)
    files: lib.dom.d.ts
- **IIRFilterNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **IdleDeadline** (parents: —)
    files: lib.dom.d.ts
- **ImageBitmap** (parents: —)
    files: lib.dom.d.ts
- **ImageBitmapRenderingContext** (parents: —)
    files: lib.dom.d.ts
- **ImageData** (parents: —)
    files: lib.dom.d.ts
- **ImageDecoder** (parents: —)
    files: lib.dom.d.ts
- **ImageTrack** (parents: —)
    files: lib.dom.d.ts
- **ImageTrackList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **InputDeviceInfo** (parents: MediaDeviceInfo)
    files: lib.dom.d.ts
- **InputEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **Instance** (parents: —)
    files: lib.dom.d.ts
- **Int16Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **Int32Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **Int8Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **IntersectionObserver** (parents: —)
    files: lib.dom.d.ts
- **IntersectionObserverEntry** (parents: —)
    files: lib.dom.d.ts
- **Iterator** (parents: —)
    files: lib.es2015.iterable.d.ts, lib.esnext.iterator.d.ts
- **KeyboardEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **KeyframeEffect** (parents: AnimationEffect)
    files: lib.dom.d.ts
- **LargestContentfulPaint** (parents: PerformanceEntry)
    files: lib.dom.d.ts
- **LinkError** (parents: Error)
    files: lib.dom.d.ts
- **ListFormat** (parents: —)
    files: lib.es2021.intl.d.ts
- **Location** (parents: —)
    files: lib.dom.d.ts
- **Lock** (parents: —)
    files: lib.dom.d.ts
- **LockManager** (parents: —)
    files: lib.dom.d.ts
- **MIDIAccess** (parents: EventTarget)
    files: lib.dom.d.ts
- **MIDIConnectionEvent** (parents: Event)
    files: lib.dom.d.ts
- **MIDIInput** (parents: MIDIPort)
    files: lib.dom.d.ts
- **MIDIInputMap** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **MIDIMessageEvent** (parents: Event)
    files: lib.dom.d.ts
- **MIDIOutput** (parents: MIDIPort)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **MIDIOutputMap** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **MIDIPort** (parents: EventTarget)
    files: lib.dom.d.ts
- **Map** (parents: —)
    files: lib.es2015.collection.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts
- **MathMLElement** (parents: Element)
    files: lib.dom.d.ts
- **MediaCapabilities** (parents: —)
    files: lib.dom.d.ts
- **MediaDeviceInfo** (parents: —)
    files: lib.dom.d.ts
- **MediaDevices** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaElementAudioSourceNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **MediaEncryptedEvent** (parents: Event)
    files: lib.dom.d.ts
- **MediaError** (parents: —)
    files: lib.dom.d.ts
- **MediaKeyMessageEvent** (parents: Event)
    files: lib.dom.d.ts
- **MediaKeySession** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaKeyStatusMap** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **MediaKeySystemAccess** (parents: —)
    files: lib.dom.d.ts
- **MediaKeys** (parents: —)
    files: lib.dom.d.ts
- **MediaList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **MediaMetadata** (parents: —)
    files: lib.dom.d.ts
- **MediaQueryList** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaQueryListEvent** (parents: Event)
    files: lib.dom.d.ts
- **MediaRecorder** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaSession** (parents: —)
    files: lib.dom.d.ts
- **MediaSource** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaSourceHandle** (parents: —)
    files: lib.dom.d.ts
- **MediaStream** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaStreamAudioDestinationNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **MediaStreamAudioSourceNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **MediaStreamTrack** (parents: EventTarget)
    files: lib.dom.d.ts
- **MediaStreamTrackEvent** (parents: Event)
    files: lib.dom.d.ts
- **Memory** (parents: —)
    files: lib.dom.d.ts
- **MessageChannel** (parents: —)
    files: lib.dom.d.ts
- **MessageEvent** (parents: Event)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **MessagePort** (parents: EventTarget)
    files: lib.dom.d.ts
- **MimeType** (parents: —)
    files: lib.dom.d.ts
- **MimeTypeArray** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **Module** (parents: —)
    files: lib.dom.d.ts
- **MouseEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **MutationObserver** (parents: —)
    files: lib.dom.d.ts
- **MutationRecord** (parents: —)
    files: lib.dom.d.ts
- **NamedNodeMap** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **NavigationActivation** (parents: —)
    files: lib.dom.d.ts
- **NavigationHistoryEntry** (parents: EventTarget)
    files: lib.dom.d.ts
- **NavigationPreloadManager** (parents: —)
    files: lib.dom.d.ts
- **Navigator** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **Node** (parents: EventTarget)
    files: lib.dom.d.ts
- **NodeIterator** (parents: —)
    files: lib.dom.d.ts
- **NodeList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **Notification** (parents: EventTarget)
    files: lib.dom.d.ts
- **Number** (parents: —)
    files: lib.es2020.number.d.ts, lib.es5.d.ts
- **NumberFormat** (parents: —)
    files: lib.es2018.intl.d.ts, lib.es2020.bigint.d.ts, lib.es2023.intl.d.ts, lib.es5.d.ts
- **Object** (parents: —)
    files: lib.es5.d.ts
- **OfflineAudioCompletionEvent** (parents: Event)
    files: lib.dom.d.ts
- **OfflineAudioContext** (parents: BaseAudioContext)
    files: lib.dom.d.ts
- **OffscreenCanvas** (parents: EventTarget)
    files: lib.dom.d.ts
- **OffscreenCanvasRenderingContext2D** (parents: —)
    files: lib.dom.d.ts
- **OscillatorNode** (parents: AudioScheduledSourceNode)
    files: lib.dom.d.ts
- **OverconstrainedError** (parents: DOMException)
    files: lib.dom.d.ts
- **PageRevealEvent** (parents: Event)
    files: lib.dom.d.ts
- **PageSwapEvent** (parents: Event)
    files: lib.dom.d.ts
- **PageTransitionEvent** (parents: Event)
    files: lib.dom.d.ts
- **PannerNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **Path2D** (parents: —)
    files: lib.dom.d.ts
- **PaymentAddress** (parents: —)
    files: lib.dom.d.ts
- **PaymentMethodChangeEvent** (parents: PaymentRequestUpdateEvent)
    files: lib.dom.d.ts
- **PaymentRequest** (parents: EventTarget)
    files: lib.dom.d.ts
- **PaymentRequestUpdateEvent** (parents: Event)
    files: lib.dom.d.ts
- **PaymentResponse** (parents: EventTarget)
    files: lib.dom.d.ts
- **Performance** (parents: EventTarget)
    files: lib.dom.d.ts
- **PerformanceEntry** (parents: —)
    files: lib.dom.d.ts
- **PerformanceEventTiming** (parents: PerformanceEntry)
    files: lib.dom.d.ts
- **PerformanceMark** (parents: PerformanceEntry)
    files: lib.dom.d.ts
- **PerformanceMeasure** (parents: PerformanceEntry)
    files: lib.dom.d.ts
- **PerformanceNavigation** (parents: —)
    files: lib.dom.d.ts
- **PerformanceNavigationTiming** (parents: PerformanceResourceTiming)
    files: lib.dom.d.ts
- **PerformanceObserver** (parents: —)
    files: lib.dom.d.ts
- **PerformanceObserverEntryList** (parents: —)
    files: lib.dom.d.ts
- **PerformancePaintTiming** (parents: PerformanceEntry)
    files: lib.dom.d.ts
- **PerformanceResourceTiming** (parents: PerformanceEntry)
    files: lib.dom.d.ts
- **PerformanceServerTiming** (parents: —)
    files: lib.dom.d.ts
- **PerformanceTiming** (parents: —)
    files: lib.dom.d.ts
- **PeriodicWave** (parents: —)
    files: lib.dom.d.ts
- **PermissionStatus** (parents: EventTarget)
    files: lib.dom.d.ts
- **Permissions** (parents: —)
    files: lib.dom.d.ts
- **PictureInPictureEvent** (parents: Event)
    files: lib.dom.d.ts
- **PictureInPictureWindow** (parents: EventTarget)
    files: lib.dom.d.ts
- **Plugin** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **PluginArray** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **PluralRules** (parents: —)
    files: lib.es2018.intl.d.ts
- **PointerEvent** (parents: MouseEvent)
    files: lib.dom.d.ts
- **PopStateEvent** (parents: Event)
    files: lib.dom.d.ts
- **ProcessingInstruction** (parents: CharacterData)
    files: lib.dom.d.ts
- **ProgressEvent** (parents: Event)
    files: lib.dom.d.ts
- **Promise** (parents: —)
    files: lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2018.promise.d.ts, lib.es5.d.ts
- **PromiseRejectionEvent** (parents: Event)
    files: lib.dom.d.ts
- **PublicKeyCredential** (parents: Credential)
    files: lib.dom.d.ts
- **PushManager** (parents: —)
    files: lib.dom.d.ts
- **PushSubscription** (parents: —)
    files: lib.dom.d.ts
- **PushSubscriptionOptions** (parents: —)
    files: lib.dom.d.ts
- **RTCCertificate** (parents: —)
    files: lib.dom.d.ts
- **RTCDTMFSender** (parents: EventTarget)
    files: lib.dom.d.ts
- **RTCDTMFToneChangeEvent** (parents: Event)
    files: lib.dom.d.ts
- **RTCDataChannel** (parents: EventTarget)
    files: lib.dom.d.ts
- **RTCDataChannelEvent** (parents: Event)
    files: lib.dom.d.ts
- **RTCDtlsTransport** (parents: EventTarget)
    files: lib.dom.d.ts
- **RTCEncodedAudioFrame** (parents: —)
    files: lib.dom.d.ts
- **RTCEncodedVideoFrame** (parents: —)
    files: lib.dom.d.ts
- **RTCError** (parents: DOMException)
    files: lib.dom.d.ts
- **RTCErrorEvent** (parents: Event)
    files: lib.dom.d.ts
- **RTCIceCandidate** (parents: —)
    files: lib.dom.d.ts
- **RTCIceTransport** (parents: EventTarget)
    files: lib.dom.d.ts
- **RTCPeerConnection** (parents: EventTarget)
    files: lib.dom.d.ts
- **RTCPeerConnectionIceErrorEvent** (parents: Event)
    files: lib.dom.d.ts
- **RTCPeerConnectionIceEvent** (parents: Event)
    files: lib.dom.d.ts
- **RTCRtpReceiver** (parents: —)
    files: lib.dom.d.ts
- **RTCRtpScriptTransform** (parents: —)
    files: lib.dom.d.ts
- **RTCRtpSender** (parents: —)
    files: lib.dom.d.ts
- **RTCRtpTransceiver** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **RTCSctpTransport** (parents: EventTarget)
    files: lib.dom.d.ts
- **RTCSessionDescription** (parents: —)
    files: lib.dom.d.ts
- **RTCStatsReport** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **RTCTrackEvent** (parents: Event)
    files: lib.dom.d.ts
- **RadioNodeList** (parents: NodeList)
    files: lib.dom.d.ts
- **Range** (parents: AbstractRange)
    files: lib.dom.d.ts
- **RangeError** (parents: Error)
    files: lib.es5.d.ts
- **ReadableByteStreamController** (parents: —)
    files: lib.dom.d.ts
- **ReadableStream** (parents: —)
    files: lib.dom.asynciterable.d.ts, lib.dom.d.ts
- **ReadableStreamBYOBReader** (parents: —)
    files: lib.dom.d.ts
- **ReadableStreamBYOBRequest** (parents: —)
    files: lib.dom.d.ts
- **ReadableStreamDefaultController** (parents: —)
    files: lib.dom.d.ts
- **ReadableStreamDefaultReader** (parents: —)
    files: lib.dom.d.ts
- **ReferenceError** (parents: Error)
    files: lib.es5.d.ts
- **RegExp** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2018.regexp.d.ts, lib.es2020.symbol.wellknown.d.ts, lib.es2022.regexp.d.ts, lib.es2024.regexp.d.ts, lib.es5.d.ts
- **RemotePlayback** (parents: EventTarget)
    files: lib.dom.d.ts
- **Report** (parents: —)
    files: lib.dom.d.ts
- **ReportBody** (parents: —)
    files: lib.dom.d.ts
- **ReportingObserver** (parents: —)
    files: lib.dom.d.ts
- **Request** (parents: —)
    files: lib.dom.d.ts
- **ResizeObserver** (parents: —)
    files: lib.dom.d.ts
- **ResizeObserverEntry** (parents: —)
    files: lib.dom.d.ts
- **ResizeObserverSize** (parents: —)
    files: lib.dom.d.ts
- **Response** (parents: —)
    files: lib.dom.d.ts
- **RuntimeError** (parents: Error)
    files: lib.dom.d.ts
- **SVGAElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGAngle** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimateElement** (parents: SVGAnimationElement)
    files: lib.dom.d.ts
- **SVGAnimateMotionElement** (parents: SVGAnimationElement)
    files: lib.dom.d.ts
- **SVGAnimateTransformElement** (parents: SVGAnimationElement)
    files: lib.dom.d.ts
- **SVGAnimatedAngle** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedBoolean** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedEnumeration** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedInteger** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedLength** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedLengthList** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedNumber** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedNumberList** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedPreserveAspectRatio** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedRect** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedString** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimatedTransformList** (parents: —)
    files: lib.dom.d.ts
- **SVGAnimationElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGCircleElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGClipPathElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGComponentTransferFunctionElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGDefsElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGDescElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGElement** (parents: Element)
    files: lib.dom.d.ts
- **SVGEllipseElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGFEBlendElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEColorMatrixElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEComponentTransferElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFECompositeElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEConvolveMatrixElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEDiffuseLightingElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEDisplacementMapElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEDistantLightElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEDropShadowElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEFloodElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEFuncAElement** (parents: SVGComponentTransferFunctionElement)
    files: lib.dom.d.ts
- **SVGFEFuncBElement** (parents: SVGComponentTransferFunctionElement)
    files: lib.dom.d.ts
- **SVGFEFuncGElement** (parents: SVGComponentTransferFunctionElement)
    files: lib.dom.d.ts
- **SVGFEFuncRElement** (parents: SVGComponentTransferFunctionElement)
    files: lib.dom.d.ts
- **SVGFEGaussianBlurElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEImageElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEMergeElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEMergeNodeElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEMorphologyElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEOffsetElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFEPointLightElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFESpecularLightingElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFESpotLightElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFETileElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFETurbulenceElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGFilterElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGForeignObjectElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGGElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGGeometryElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGGradientElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGGraphicsElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGImageElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGLength** (parents: —)
    files: lib.dom.d.ts
- **SVGLengthList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SVGLineElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGLinearGradientElement** (parents: SVGGradientElement)
    files: lib.dom.d.ts
- **SVGMPathElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGMarkerElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGMaskElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGMetadataElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGNumber** (parents: —)
    files: lib.dom.d.ts
- **SVGNumberList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SVGPathElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGPatternElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGPointList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SVGPolygonElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGPolylineElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGPreserveAspectRatio** (parents: —)
    files: lib.dom.d.ts
- **SVGRadialGradientElement** (parents: SVGGradientElement)
    files: lib.dom.d.ts
- **SVGRectElement** (parents: SVGGeometryElement)
    files: lib.dom.d.ts
- **SVGSVGElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGScriptElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGSetElement** (parents: SVGAnimationElement)
    files: lib.dom.d.ts
- **SVGStopElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGStringList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SVGStyleElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGSwitchElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGSymbolElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGTSpanElement** (parents: SVGTextPositioningElement)
    files: lib.dom.d.ts
- **SVGTextContentElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGTextElement** (parents: SVGTextPositioningElement)
    files: lib.dom.d.ts
- **SVGTextPathElement** (parents: SVGTextContentElement)
    files: lib.dom.d.ts
- **SVGTextPositioningElement** (parents: SVGTextContentElement)
    files: lib.dom.d.ts
- **SVGTitleElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **SVGTransform** (parents: —)
    files: lib.dom.d.ts
- **SVGTransformList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SVGUnitTypes** (parents: —)
    files: lib.dom.d.ts
- **SVGUseElement** (parents: SVGGraphicsElement)
    files: lib.dom.d.ts
- **SVGViewElement** (parents: SVGElement)
    files: lib.dom.d.ts
- **Screen** (parents: —)
    files: lib.dom.d.ts
- **ScreenOrientation** (parents: EventTarget)
    files: lib.dom.d.ts
- **ScriptProcessorNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **SecurityPolicyViolationEvent** (parents: Event)
    files: lib.dom.d.ts
- **Segmenter** (parents: —)
    files: lib.es2022.intl.d.ts
- **Selection** (parents: —)
    files: lib.dom.d.ts
- **ServiceWorker** (parents: EventTarget)
    files: lib.dom.d.ts
- **ServiceWorkerContainer** (parents: EventTarget)
    files: lib.dom.d.ts
- **ServiceWorkerRegistration** (parents: EventTarget)
    files: lib.dom.d.ts
- **Set** (parents: —)
    files: lib.es2015.collection.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.esnext.collection.d.ts
- **ShadowRoot** (parents: DocumentFragment)
    files: lib.dom.d.ts
- **SharedArrayBuffer** (parents: —)
    files: lib.es2017.sharedmemory.d.ts, lib.es2024.sharedmemory.d.ts
- **SharedWorker** (parents: EventTarget)
    files: lib.dom.d.ts
- **SourceBuffer** (parents: EventTarget)
    files: lib.dom.d.ts
- **SourceBufferList** (parents: EventTarget)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SpeechRecognitionAlternative** (parents: —)
    files: lib.dom.d.ts
- **SpeechRecognitionResult** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SpeechRecognitionResultList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SpeechSynthesis** (parents: EventTarget)
    files: lib.dom.d.ts
- **SpeechSynthesisErrorEvent** (parents: SpeechSynthesisEvent)
    files: lib.dom.d.ts
- **SpeechSynthesisEvent** (parents: Event)
    files: lib.dom.d.ts
- **SpeechSynthesisUtterance** (parents: EventTarget)
    files: lib.dom.d.ts
- **SpeechSynthesisVoice** (parents: —)
    files: lib.dom.d.ts
- **StaticRange** (parents: AbstractRange)
    files: lib.dom.d.ts
- **StereoPannerNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **Storage** (parents: —)
    files: lib.dom.d.ts
- **StorageEvent** (parents: Event)
    files: lib.dom.d.ts
- **StorageManager** (parents: —)
    files: lib.dom.d.ts
- **String** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2017.string.d.ts, lib.es2019.string.d.ts, lib.es2020.string.d.ts, lib.es2021.string.d.ts, lib.es2022.string.d.ts, lib.es2024.string.d.ts, lib.es5.d.ts
- **StylePropertyMap** (parents: StylePropertyMapReadOnly)
    files: lib.dom.d.ts
- **StylePropertyMapReadOnly** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **StyleSheet** (parents: —)
    files: lib.dom.d.ts
- **StyleSheetList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SubmitEvent** (parents: Event)
    files: lib.dom.d.ts
- **SubtleCrypto** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **SuppressedError** (parents: Error)
    files: lib.esnext.disposable.d.ts
- **Symbol** (parents: —)
    files: lib.es2015.symbol.wellknown.d.ts, lib.es2019.symbol.d.ts, lib.es5.d.ts
- **SyntaxError** (parents: Error)
    files: lib.es5.d.ts
- **Table** (parents: —)
    files: lib.dom.d.ts
- **Text** (parents: CharacterData)
    files: lib.dom.d.ts
- **TextDecoder** (parents: —)
    files: lib.dom.d.ts
- **TextDecoderStream** (parents: —)
    files: lib.dom.d.ts
- **TextEncoder** (parents: —)
    files: lib.dom.d.ts
- **TextEncoderStream** (parents: —)
    files: lib.dom.d.ts
- **TextEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **TextMetrics** (parents: —)
    files: lib.dom.d.ts
- **TextTrack** (parents: EventTarget)
    files: lib.dom.d.ts
- **TextTrackCue** (parents: EventTarget)
    files: lib.dom.d.ts
- **TextTrackCueList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **TextTrackList** (parents: EventTarget)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **TimeRanges** (parents: —)
    files: lib.dom.d.ts
- **ToggleEvent** (parents: Event)
    files: lib.dom.d.ts
- **Touch** (parents: —)
    files: lib.dom.d.ts
- **TouchEvent** (parents: UIEvent)
    files: lib.dom.d.ts
- **TouchList** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **TrackEvent** (parents: Event)
    files: lib.dom.d.ts
- **TransformStream** (parents: —)
    files: lib.dom.d.ts
- **TransformStreamDefaultController** (parents: —)
    files: lib.dom.d.ts
- **TransitionEvent** (parents: Event)
    files: lib.dom.d.ts
- **TreeWalker** (parents: —)
    files: lib.dom.d.ts
- **TypeError** (parents: Error)
    files: lib.es5.d.ts
- **UIEvent** (parents: Event)
    files: lib.dom.d.ts
- **URIError** (parents: Error)
    files: lib.es5.d.ts
- **URL** (parents: —)
    files: lib.dom.d.ts
- **URLSearchParams** (parents: —)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **Uint16Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **Uint32Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **Uint8Array** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **Uint8ClampedArray** (parents: —)
    files: lib.es2015.core.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts, lib.es2016.array.include.d.ts, lib.es2022.array.d.ts, lib.es2023.array.d.ts, lib.es5.d.ts
- **UserActivation** (parents: —)
    files: lib.dom.d.ts
- **VBArray** (parents: —)
    files: lib.scripthost.d.ts
- **VTTCue** (parents: TextTrackCue)
    files: lib.dom.d.ts
- **VTTRegion** (parents: —)
    files: lib.dom.d.ts
- **ValidityState** (parents: —)
    files: lib.dom.d.ts
- **VideoColorSpace** (parents: —)
    files: lib.dom.d.ts
- **VideoDecoder** (parents: EventTarget)
    files: lib.dom.d.ts
- **VideoEncoder** (parents: EventTarget)
    files: lib.dom.d.ts
- **VideoFrame** (parents: —)
    files: lib.dom.d.ts
- **VideoPlaybackQuality** (parents: —)
    files: lib.dom.d.ts
- **ViewTransition** (parents: —)
    files: lib.dom.d.ts
- **ViewTransitionTypeSet** (parents: Set)
    files: lib.dom.d.ts, lib.dom.iterable.d.ts
- **VisualViewport** (parents: EventTarget)
    files: lib.dom.d.ts
- **WakeLock** (parents: —)
    files: lib.dom.d.ts
- **WakeLockSentinel** (parents: EventTarget)
    files: lib.dom.d.ts
- **WaveShaperNode** (parents: AudioNode)
    files: lib.dom.d.ts
- **WeakMap** (parents: —)
    files: lib.es2015.collection.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts
- **WeakRef** (parents: —)
    files: lib.es2021.weakref.d.ts
- **WeakSet** (parents: —)
    files: lib.es2015.collection.d.ts, lib.es2015.iterable.d.ts, lib.es2015.symbol.wellknown.d.ts
- **WebGL2RenderingContext** (parents: —)
    files: lib.dom.d.ts
- **WebGLActiveInfo** (parents: —)
    files: lib.dom.d.ts
- **WebGLBuffer** (parents: —)
    files: lib.dom.d.ts
- **WebGLContextEvent** (parents: Event)
    files: lib.dom.d.ts
- **WebGLFramebuffer** (parents: —)
    files: lib.dom.d.ts
- **WebGLProgram** (parents: —)
    files: lib.dom.d.ts
- **WebGLQuery** (parents: —)
    files: lib.dom.d.ts
- **WebGLRenderbuffer** (parents: —)
    files: lib.dom.d.ts
- **WebGLRenderingContext** (parents: —)
    files: lib.dom.d.ts
- **WebGLSampler** (parents: —)
    files: lib.dom.d.ts
- **WebGLShader** (parents: —)
    files: lib.dom.d.ts
- **WebGLShaderPrecisionFormat** (parents: —)
    files: lib.dom.d.ts
- **WebGLSync** (parents: —)
    files: lib.dom.d.ts
- **WebGLTexture** (parents: —)
    files: lib.dom.d.ts
- **WebGLTransformFeedback** (parents: —)
    files: lib.dom.d.ts
- **WebGLUniformLocation** (parents: —)
    files: lib.dom.d.ts
- **WebGLVertexArrayObject** (parents: —)
    files: lib.dom.d.ts
- **WebSocket** (parents: EventTarget)
    files: lib.dom.d.ts
- **WebTransport** (parents: —)
    files: lib.dom.d.ts
- **WebTransportBidirectionalStream** (parents: —)
    files: lib.dom.d.ts
- **WebTransportDatagramDuplexStream** (parents: —)
    files: lib.dom.d.ts
- **WebTransportError** (parents: DOMException)
    files: lib.dom.d.ts
- **WheelEvent** (parents: MouseEvent)
    files: lib.dom.d.ts
- **Window** (parents: EventTarget)
    files: lib.dom.d.ts
- **Worker** (parents: EventTarget)
    files: lib.dom.d.ts
- **Worklet** (parents: —)
    files: lib.dom.d.ts
- **WritableStream** (parents: —)
    files: lib.dom.d.ts
- **WritableStreamDefaultController** (parents: —)
    files: lib.dom.d.ts
- **WritableStreamDefaultWriter** (parents: —)
    files: lib.dom.d.ts
- **XMLDocument** (parents: Document)
    files: lib.dom.d.ts
- **XMLHttpRequest** (parents: XMLHttpRequestEventTarget)
    files: lib.dom.d.ts
- **XMLHttpRequestEventTarget** (parents: EventTarget)
    files: lib.dom.d.ts
- **XMLHttpRequestUpload** (parents: XMLHttpRequestEventTarget)
    files: lib.dom.d.ts
- **XMLSerializer** (parents: —)
    files: lib.dom.d.ts
- **XPathEvaluator** (parents: —)
    files: lib.dom.d.ts
- **XPathExpression** (parents: —)
    files: lib.dom.d.ts
- **XPathResult** (parents: —)
    files: lib.dom.d.ts
- **XSLTProcessor** (parents: —)
    files: lib.dom.d.ts
