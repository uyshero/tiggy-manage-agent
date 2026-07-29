# TMA Biography Voice native plugin

This directory contains the source for the UniApp native voice module.

- `android`: Kotlin `UniModule`, full-duplex 16 kHz PCM16 capture with echo
  cancellation, 24 kHz PCM16 playback, VAD, interview-length private WAV storage, and encrypted preferences.
- `ios`: Objective-C `DCUniModule` adapter plus a Swift core using
  `URLSessionWebSocketTask`, voice-chat `AVAudioEngine`, interview-length private WAV storage,
  `AVAudioPlayerNode`, and Keychain.
- `plugin-package`: DCloud plugin manifest and destination for compiled native
  artifacts.

The mobile App calls the same methods and receives the same event objects on
both platforms. Long-lived TMA and Doubao credentials remain in the Go voice
gateway. See `../../VOICE_PLUGIN.md` for the contract and build instructions.

One press of **Start interview** opens one recording session. Each recognized
answer is appended to the same WAV, and **Stop interview** seals it. Interviewer
TTS and unrecognized silence are never written to the recording.
