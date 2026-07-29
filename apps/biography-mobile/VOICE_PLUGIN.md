# Biography Voice Plugin Contract

The UniApp layer loads the native plugin as `TMA-BiographyVoice`. API keys and
Doubao endpoints must remain in the server-side voice gateway; the plugin only
connects to the application's authenticated gateway.

## Commands

- `configure({ gatewayURL, shortLivedToken? }, callback)`
- `startListening({ sampleRate, channelCount }, callback)`
- `stopListening(callback)`
- `playText({ text, expression }, callback)`
- `cancelPlayback(callback)`
- `finishRecordingSession(callback)`
- `deleteRecording({ filePath }, callback)`
- `dispose(callback)`

Callbacks return `{ ok: boolean, message?: string }`.

`configure` is lazy: UniApp invokes it immediately before the first voice
operation. `gatewayURL` is allowed in App build configuration because it is not
a secret. `shortLivedToken` must come from the signed-in App session and is held
in native memory only. It must not be written to a `VITE_*` variable or native
preferences.

## Events

- `partial_transcript`: `{ text }`
- `final_transcript`: `{ text }`
- `project_loaded`: `{ project }`
- `assistant_reply`: `{ text, expression, project }`
- `playback_started`
- `playback_finished`
- `speech_detected`
- `recording_ready`: `{ filePath, durationMs, sizeBytes, transcript, cumulative }`
- `network_lost`
- `network_restored`
- `error`: `{ message, code? }`；`no_speech` 会由页面语音提示后自动重新倾听

Listening turns with no detected speech are committed after 30 seconds. The
page retries twice with a spoken reminder, then speaks a short rest message and
automatically pauses after the third consecutive `no_speech`. Any valid speech
or manual resume clears the consecutive timeout count.

The Kotlin and Swift implementations own microphone permission, VAD, audio
focus, interruption handling, buffering, and reconnect behavior. A browser
mock implements the same interface for product review without credentials.

During TTS playback, native capture runs in monitor mode with acoustic echo
cancellation. Monitor audio is not uploaded or saved. Sustained user speech
emits `speech_detected`; after the page cancels playback and calls
`startListening`, up to 1.5 seconds of pre-roll is sent so the beginning of the
interruption is retained. Only committed listening turns are appended to the
current interview WAV in the App-private recording directory. Every
`recording_ready` event points to that same file and reports its cumulative
duration and size. Calling `finishRecordingSession` seals the file; the next
interview starts a new WAV.

The native implementations must persist `client_instance_id` and the encrypted
gateway `resume_token` in EncryptedSharedPreferences on Android and Keychain on
iOS. H5 uses UniApp storage for local protocol testing only. The plugin must
never persist a Doubao key, a TMA bearer token, or a plaintext TMA session ID.

Production builds accept only `wss://` gateway URLs. Debug builds also accept
`ws://` so a physical device or simulator can connect to a local gateway.

## Native source and packaging

The implementation source is under `native/TMA-BiographyVoice`. It is not a
cloud-package-ready plugin until both native artifacts below have been built
against the same DCloud offline SDK version as the App.

Android:

1. Copy `uniapp-v8-release.aar` from the matching DCloud Android offline SDK to
   `native/TMA-BiographyVoice/android/libs/`.
2. Run `gradle assembleRelease` in the `android` directory, using JDK 17 and an
   installed Android SDK with API 35.
3. Copy `build/outputs/aar/android-release.aar` to
   `plugin-package/android/TMA-BiographyVoice.aar`.

iOS:

1. Create a static Framework target named `TMA_BiographyVoice`, deployment
   target iOS 13, and add all files from `native/TMA-BiographyVoice/ios`.
2. Add the matching DCloud iOS offline SDK headers/libraries so
   `DCUniModule.h` resolves. Link `AVFoundation`, `AudioToolbox`, and `Security`.
3. Build the Release device/arm64 framework and copy
   `TMA_BiographyVoice.framework` to `plugin-package/ios/`.

Finally copy the completed `plugin-package` directory to
`nativeplugins/TMA-BiographyVoice` before the UniApp App build. Do not copy the
source-only package: DCloud requires the compiled AAR and static framework.

## Local gateway mode

For H5 integration testing, copy `.env.example` to `.env.local` and run the Go
voice gateway. Set `VITE_BIOGRAPHY_VOICE_DEBUG_TEXT=false` to capture browser
microphone audio, resample it to 16 kHz PCM16, and play the gateway's 24 kHz
PCM16 response. Browser microphone access requires HTTPS or localhost and user
permission. Production H5 must use `wss://`.

Set `VITE_BIOGRAPHY_VOICE_DEBUG_TEXT=true` only when testing without a
microphone. It exercises the same control protocol with sample text. Never
embed a long-lived gateway or Doubao token in a `VITE_*` variable.
