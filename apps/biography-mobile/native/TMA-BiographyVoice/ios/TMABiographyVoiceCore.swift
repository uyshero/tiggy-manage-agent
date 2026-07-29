import AVFoundation
import Foundation
import Security

@objcMembers
public final class TMABiographyVoiceCore: NSObject, URLSessionWebSocketDelegate {
    public var eventHandler: ((NSDictionary) -> Void)?

    private let stateQueue = DispatchQueue(label: "com.tma.biography.voice.state")
    private let playbackQueue = DispatchQueue(label: "com.tma.biography.voice.playback")
    private var session: URLSession!
    private var socket: URLSessionWebSocketTask?
    private var gatewayURL: URL?
    private var accessToken = ""
    private var configureCompletion: ((NSDictionary) -> Void)?
    private var connected = false
    private var disposed = false
    private var reconnectAttempt = 0
    private var sessionID = ""

    private let captureEngine = AVAudioEngine()
    private var captureConverter: AVAudioConverter?
    private var capturing = false
    private var captureMode = CaptureMode.none
    private var manualCommit = false
    private var deferInterviewOnNextCommit = false
    private var speechDetected = false
    private var candidateSpeechDuration = 0.0
    private var captureAudioSent = false
    private var captureStartedAt = Date()
    private var lastVoiceAt = Date()
    private var interruptionDetected = false
    private var preRoll: [Data] = []
    private var preRollBytes = 0
    private var capturedPCM = Data()
    private var pendingRecording: PendingRecording?
    private var sessionRecordingURL: URL?
    private var sessionRecordingPCMBytes = 0
    private var finishRecordingAfterPending = false

    private let speechGraceInterval = 0.45
    private let minimumSpeechDuration = 0.12
    private let silenceInterval = 1.8
    private let noSpeechTimeoutInterval = 30.0
    private let voiceThreshold = 330.0
    private let interruptionGraceInterval = 0.7
    private let interruptionMinimumSpeechDuration = 0.18
    private let interruptionThreshold = 500.0
    private let preRollMaxBytes = 48_000

    private let playbackEngine = AVAudioEngine()
    private let playerNode = AVAudioPlayerNode()
    private let playbackFormat = AVAudioFormat(
        commonFormat: .pcmFormatInt16,
        sampleRate: 24_000,
        channels: 1,
        interleaved: false
    )!
    private var playbackGeneration = 0
    private var pendingPlaybackBuffers = 0
    private var serverFinishedPlayback = false

    private static let keychainService = "com.tma.biography.voice"
    private static let clientInstanceAccount = "client_instance_id"
    private static let resumeTokenAccount = "resume_token"

    private var recordingDirectory: URL {
        let base = FileManager.default.urls(for: .applicationSupportDirectory, in: .userDomainMask).first!
        return base.appendingPathComponent("BiographyRecordings", isDirectory: true).standardizedFileURL
    }

    public override init() {
        super.init()
        let configuration = URLSessionConfiguration.default
        configuration.timeoutIntervalForRequest = 20
        session = URLSession(configuration: configuration, delegate: self, delegateQueue: nil)
        NotificationCenter.default.addObserver(
            self,
            selector: #selector(audioSessionInterrupted(_:)),
            name: AVAudioSession.interruptionNotification,
            object: AVAudioSession.sharedInstance()
        )
    }

    deinit {
        NotificationCenter.default.removeObserver(self)
        session.invalidateAndCancel()
    }

    public func configure(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        let rawURL = (options["gatewayURL"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
        guard let url = URL(string: rawURL), validGatewayURL(url) else {
            completion(result(false, "请先配置安全的语音网关地址"))
            return
        }
        stateQueue.async {
            self.disposed = false
            self.gatewayURL = url
            self.accessToken = (options["shortLivedToken"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            self.configureCompletion = completion
            self.socket?.cancel(with: .goingAway, reason: nil)
            self.socket = nil
            self.connected = false
            self.connect()
        }
    }

    public func startListening(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            guard self.connected else {
                self.complete(completion, false, "语音服务正在重新连接，请稍后再试")
                return
            }
            if self.capturing {
                if self.captureMode == .monitor { self.promoteCapture() }
                self.complete(completion, true, nil)
                return
            }
            let requestedRate = (options["sampleRate"] as? NSNumber)?.intValue ?? 16_000
            guard requestedRate == 16_000 else {
                self.complete(completion, false, "当前仅支持 16000Hz 录音")
                return
            }
            AVAudioSession.sharedInstance().requestRecordPermission { allowed in
                self.stateQueue.async {
                    guard allowed else {
                        self.complete(completion, false, "请允许麦克风权限后再次点击开始讲述")
                        return
                    }
                    do {
                        self.manualCommit = ((options["manualCommit"] as? NSNumber)?.boolValue ?? false)
                        try self.startCapture(mode: .listening, stopPlayback: true)
                        self.complete(completion, true, nil)
                    } catch {
                        self.complete(completion, false, "当前设备无法打开麦克风")
                    }
                }
            }
        }
    }

    public func stopListening(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            self.deferInterviewOnNextCommit = ((options["deferInterview"] as? NSNumber)?.boolValue ?? false)
            self.stopCapture(commit: true)
            self.complete(completion, true, nil)
        }
    }

    public func cancelListening(_ completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            self.stopCapture(commit: false)
            if self.connected {
                self.sendJSON(["type": "input.cancel", "session_id": self.sessionID])
            }
            self.complete(completion, true, nil)
        }
    }

    public func requestFollowup(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            let text = (options["text"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard self.connected, !text.isEmpty else {
                self.complete(completion, false, text.isEmpty ? "没有可提交的采访内容" : "语音服务正在重新连接")
                return
            }
            self.sendJSON(["type": "interview.followup", "session_id": self.sessionID, "text": text])
            self.complete(completion, true, nil)
        }
    }

    public func setInterviewOrder(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            let order = (options["interviewOrder"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard self.connected else {
                self.complete(completion, false, "语音服务正在重新连接")
                return
            }
            guard ["chronological", "key_moments", "custom"].contains(order) else {
                self.complete(completion, false, "采访方式无效")
                return
            }
            self.sendJSON(["type": "interview.order.set", "session_id": self.sessionID, "interview_order": order])
            self.complete(completion, true, nil)
        }
    }

    public func playText(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            let text = (options["text"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            guard self.connected, !text.isEmpty else {
                self.complete(completion, false, text.isEmpty ? "没有可播放的采访内容" : "语音服务正在重新连接")
                return
            }
            self.stopCapture(commit: false)
            self.sendJSON([
                "type": "tts.start",
                "session_id": self.sessionID,
                "text": text,
                "expression": (options["expression"] as? String) ?? "",
            ])
            self.complete(completion, true, nil)
        }
    }

    public func cancelPlayback(_ completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            self.stopPlayback()
            if self.connected {
                self.sendJSON(["type": "tts.cancel", "session_id": self.sessionID])
            }
            self.complete(completion, true, nil)
        }
    }

    public func finishRecordingSession(_ completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            if self.pendingRecording == nil {
                self.resetRecordingSession()
            } else {
                self.finishRecordingAfterPending = true
            }
            self.complete(completion, true, nil)
        }
    }

    public func deleteRecording(_ options: NSDictionary, completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            let rawPath = (options["filePath"] as? String)?.trimmingCharacters(in: .whitespacesAndNewlines) ?? ""
            let target = URL(string: rawPath)?.standardizedFileURL ?? URL(fileURLWithPath: rawPath).standardizedFileURL
            guard target.path.hasPrefix(self.recordingDirectory.path + "/") else {
                self.complete(completion, false, "录音文件地址无效")
                return
            }
            do {
                if FileManager.default.fileExists(atPath: target.path) {
                    try FileManager.default.removeItem(at: target)
                }
                self.complete(completion, true, nil)
            } catch {
                self.complete(completion, false, "录音文件删除失败")
            }
        }
    }

    public func dispose(_ completion: @escaping (NSDictionary) -> Void) {
        stateQueue.async {
            self.disposed = true
            self.stopCapture(commit: false)
            self.discardPendingRecording()
            self.resetRecordingSession()
            self.stopPlayback()
            if self.connected {
                self.sendJSON(["type": "session.finish", "session_id": self.sessionID])
            }
            self.socket?.cancel(with: .normalClosure, reason: nil)
            self.socket = nil
            self.connected = false
            self.configureCompletion = nil
            self.complete(completion, true, nil)
        }
    }

    private func connect() {
        guard !disposed, let gatewayURL else { return }
        var request = URLRequest(url: gatewayURL)
        request.timeoutInterval = 20
        if !accessToken.isEmpty {
            request.setValue("Bearer \(accessToken)", forHTTPHeaderField: "Authorization")
        }
        let task = session.webSocketTask(with: request)
        socket = task
        task.resume()
        receiveNext(task)
    }

    public func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didOpenWithProtocol protocol: String?
    ) {
        stateQueue.async {
            guard webSocketTask === self.socket, !self.disposed else {
                webSocketTask.cancel(with: .normalClosure, reason: nil)
                return
            }
            let wasReconnecting = self.reconnectAttempt > 0
            self.connected = true
            self.reconnectAttempt = 0
            self.sessionID = "voice-\(UUID().uuidString.lowercased())"
            self.startGatewaySession()
            self.finishConfigure(true, nil)
            if wasReconnecting { self.emit("network_restored") }
        }
    }

    public func urlSession(
        _ session: URLSession,
        webSocketTask: URLSessionWebSocketTask,
        didCloseWith closeCode: URLSessionWebSocketTask.CloseCode,
        reason: Data?
    ) {
        stateQueue.async { self.handleDisconnect(webSocketTask, nil) }
    }

    public func urlSession(
        _ session: URLSession,
        task: URLSessionTask,
        didCompleteWithError error: Error?
    ) {
        guard let webSocketTask = task as? URLSessionWebSocketTask else { return }
        stateQueue.async { self.handleDisconnect(webSocketTask, error) }
    }

    private func receiveNext(_ task: URLSessionWebSocketTask) {
        task.receive { [weak self, weak task] result in
            guard let self, let task else { return }
            self.stateQueue.async {
                guard task === self.socket, !self.disposed else { return }
                switch result {
                case .success(.string(let text)):
                    self.handleServerMessage(text)
                    self.receiveNext(task)
                case .success(.data(let data)):
                    self.enqueuePlayback(data)
                    self.receiveNext(task)
                case .failure(let error):
                    self.handleDisconnect(task, error)
                @unknown default:
                    self.handleDisconnect(task, nil)
                }
            }
        }
    }

    private func handleDisconnect(_ task: URLSessionWebSocketTask, _ error: Error?) {
        guard task === socket else { return }
        let wasConnected = connected
        connected = false
        socket = nil
        stopCapture(commit: false)
        discardPendingRecording()
        if finishRecordingAfterPending { resetRecordingSession() }
        stopPlayback()
        guard !disposed else { return }
        if configureCompletion != nil { finishConfigure(false, "无法连接语音服务") }
        if wasConnected { emit("network_lost") }
        if error != nil, reconnectAttempt == 0 { emitError("语音连接中断，正在重新连接") }
        let delay = min(30.0, pow(2.0, Double(min(reconnectAttempt, 5))))
        reconnectAttempt += 1
        stateQueue.asyncAfter(deadline: .now() + delay) { self.connect() }
    }

    private func startGatewaySession() {
        var message: [String: Any] = [
            "type": "session.start",
            "session_id": sessionID,
            "client_instance_id": clientInstanceID(),
        ]
        if let token = Self.readKeychain(Self.resumeTokenAccount), !token.isEmpty {
            message["resume_token"] = token
        }
        sendJSON(message)
    }

    private func handleServerMessage(_ text: String) {
        guard
            let data = text.data(using: .utf8),
            let message = try? JSONSerialization.jsonObject(with: data) as? [String: Any],
            let type = message["type"] as? String
        else {
            emitError("语音服务返回了无法识别的数据")
            return
        }
        switch type {
        case "asr.partial":
            emit("partial_transcript", ["text": message["text"] as? String ?? ""])
        case "asr.final":
            let transcript = message["text"] as? String ?? ""
            emit("final_transcript", ["text": transcript])
            resolvePendingRecording(transcript)
        case "interview.project", "interview.project.updated":
            if let token = message["resume_token"] as? String, !token.isEmpty {
                Self.writeKeychain(Self.resumeTokenAccount, token)
            }
            if let project = message["project"] { emit("project_loaded", ["project": project]) }
        case "interview.reply":
            if let token = message["resume_token"] as? String, !token.isEmpty {
                Self.writeKeychain(Self.resumeTokenAccount, token)
            }
            emit("assistant_reply", [
                "text": message["text"] as? String ?? "",
                "expression": message["expression"] as? String ?? "",
                "project": message["project"] ?? [:],
            ])
        case "tts.started":
            preparePlayback()
            emit("playback_started")
        case "tts.finished":
            playbackQueue.async {
                self.serverFinishedPlayback = true
                self.finishPlaybackIfReady()
            }
        case "tts.canceled":
            stopPlayback()
        case "error":
            discardPendingRecording()
            if finishRecordingAfterPending { resetRecordingSession() }
            let code = message["code"] as? String
            if code == "resume_invalid" {
                Self.deleteKeychain(Self.resumeTokenAccount)
                startGatewaySession()
            }
            emitError(message["message"] as? String ?? "语音服务暂时不可用", code: code)
        default:
            break
        }
    }

    private func startCapture(mode: CaptureMode, stopPlayback: Bool) throws {
        if stopPlayback { playbackQueue.sync { stopPlaybackOnQueue() } }
        let audioSession = AVAudioSession.sharedInstance()
        try audioSession.setCategory(.playAndRecord, mode: .voiceChat, options: [.defaultToSpeaker, .allowBluetooth])
        try audioSession.setActive(true)
        let input = captureEngine.inputNode
        try? input.setVoiceProcessingEnabled(true)
        let inputFormat = input.outputFormat(forBus: 0)
        guard let outputFormat = AVAudioFormat(
            commonFormat: .pcmFormatInt16,
            sampleRate: 16_000,
            channels: 1,
            interleaved: false
        ), let converter = AVAudioConverter(from: inputFormat, to: outputFormat) else {
            throw VoiceError.audioFormat
        }
        captureConverter = converter
        captureMode = mode
        speechDetected = false
        candidateSpeechDuration = 0
        captureAudioSent = false
        captureStartedAt = Date()
        lastVoiceAt = captureStartedAt
        interruptionDetected = false
        clearPreRoll()
        capturedPCM = Data()
        input.installTap(onBus: 0, bufferSize: 1_600, format: inputFormat) { [weak self] buffer, _ in
            self?.consumeCaptureBuffer(buffer, converter: converter, outputFormat: outputFormat)
        }
        captureEngine.prepare()
        do {
            try captureEngine.start()
            capturing = true
        } catch {
            input.removeTap(onBus: 0)
            captureEngine.stop()
            captureConverter = nil
            throw error
        }
    }

    private func consumeCaptureBuffer(
        _ inputBuffer: AVAudioPCMBuffer,
        converter: AVAudioConverter,
        outputFormat: AVAudioFormat
    ) {
        let capacity = AVAudioFrameCount(ceil(Double(inputBuffer.frameLength) * 16_000.0 / inputBuffer.format.sampleRate)) + 1
        guard let output = AVAudioPCMBuffer(pcmFormat: outputFormat, frameCapacity: capacity) else { return }
        var consumed = false
        var conversionError: NSError?
        let status = converter.convert(to: output, error: &conversionError) { _, inputStatus in
            if consumed {
                inputStatus.pointee = .noDataNow
                return nil
            }
            consumed = true
            inputStatus.pointee = .haveData
            return inputBuffer
        }
        guard status != .error, conversionError == nil, output.frameLength > 0, let samples = output.int16ChannelData?[0] else { return }
        let count = Int(output.frameLength)
        let data = Data(bytes: samples, count: count * MemoryLayout<Int16>.size)
        var energy = 0.0
        for index in 0..<count {
            let value = Double(samples[index])
            energy += value * value
        }
        let rootMeanSquare = sqrt(energy / Double(count))
        stateQueue.async {
            guard self.capturing, self.connected else { return }
            let now = Date()
            let grace = self.captureMode == .monitor ? self.interruptionGraceInterval : self.speechGraceInterval
            guard now.timeIntervalSince(self.captureStartedAt) >= grace else { return }
            if self.captureMode == .listening {
                self.socket?.send(.data(data)) { _ in }
                self.captureAudioSent = true
                self.capturedPCM.append(data)
            } else if self.captureMode == .monitor {
                self.appendPreRoll(data)
            }
            let threshold = self.captureMode == .monitor ? self.interruptionThreshold : self.voiceThreshold
            let requiredSpeech = self.captureMode == .monitor ? self.interruptionMinimumSpeechDuration : self.minimumSpeechDuration
            if rootMeanSquare >= threshold {
                self.candidateSpeechDuration += Double(count) / 16_000.0
                self.lastVoiceAt = now
                if !self.speechDetected, self.candidateSpeechDuration >= requiredSpeech {
                    self.speechDetected = true
                    if self.captureMode == .monitor { self.interruptionDetected = true }
                    self.emit("speech_detected")
                }
            } else if !self.speechDetected {
                self.candidateSpeechDuration = 0
            }
            if self.captureMode == .listening, !self.manualCommit {
                let silenceEnded = self.speechDetected && now.timeIntervalSince(self.lastVoiceAt) >= self.silenceInterval
                let noSpeechTimedOut = !self.speechDetected && now.timeIntervalSince(self.captureStartedAt) >= self.noSpeechTimeoutInterval
                if silenceEnded || noSpeechTimedOut || now.timeIntervalSince(self.captureStartedAt) >= 90 {
                    self.stopCapture(commit: true)
                }
            } else if self.captureMode == .listening, now.timeIntervalSince(self.captureStartedAt) >= 90 {
                self.stopCapture(commit: true)
            }
        }
    }

    private func promoteCapture() {
        guard capturing, captureMode == .monitor else { return }
        captureMode = .listening
        captureStartedAt = Date()
        if interruptionDetected {
            for audio in preRoll {
                socket?.send(.data(audio)) { _ in }
                capturedPCM.append(audio)
            }
            captureAudioSent = preRollBytes > 0
        } else {
            speechDetected = false
            candidateSpeechDuration = 0
            lastVoiceAt = captureStartedAt
            capturedPCM = Data()
        }
        clearPreRoll()
    }

    private func appendPreRoll(_ audio: Data) {
        preRoll.append(audio)
        preRollBytes += audio.count
        while preRollBytes > preRollMaxBytes, preRoll.count > 1 {
            preRollBytes -= preRoll.removeFirst().count
        }
    }

    private func clearPreRoll() {
        preRoll.removeAll(keepingCapacity: true)
        preRollBytes = 0
    }

    private func stopCapture(commit: Bool) {
        guard capturing else { return }
        capturing = false
        captureEngine.inputNode.removeTap(onBus: 0)
        captureEngine.stop()
        captureConverter = nil
        let shouldCommit = commit && captureMode == .listening && connected && captureAudioSent
        if shouldCommit, !capturedPCM.isEmpty {
            pendingRecording = writeRecording(capturedPCM)
        }
        captureAudioSent = false
        captureMode = .none
        let deferInterview = deferInterviewOnNextCommit
        manualCommit = false
        deferInterviewOnNextCommit = false
        interruptionDetected = false
        clearPreRoll()
        capturedPCM = Data()
        if shouldCommit {
            sendJSON(["type": "input.commit", "session_id": sessionID, "defer_interview": deferInterview])
        }
    }

    private func writeRecording(_ pcm: Data) -> PendingRecording? {
        guard !pcm.isEmpty else { return nil }
        return PendingRecording(pcm: pcm)
    }

    private func resolvePendingRecording(_ rawTranscript: String) {
        let transcript = rawTranscript.trimmingCharacters(in: .whitespacesAndNewlines)
        let pending = pendingRecording
        pendingRecording = nil
        if let pending, !transcript.isEmpty, let recording = appendToSessionRecording(pending) {
            emit("recording_ready", [
                "filePath": recording.url.absoluteString,
                "durationMs": recording.durationMs,
                "sizeBytes": recording.sizeBytes,
                "transcript": transcript,
                "cumulative": true,
            ])
        }
        if finishRecordingAfterPending { resetRecordingSession() }
    }

    private func appendToSessionRecording(_ pending: PendingRecording) -> SessionRecording? {
        do {
            try FileManager.default.createDirectory(at: recordingDirectory, withIntermediateDirectories: true)
            let url: URL
            if let existing = sessionRecordingURL {
                url = existing
            } else {
                url = recordingDirectory.appendingPathComponent("interview-\(Int(Date().timeIntervalSince1970 * 1_000))-\(UUID().uuidString.lowercased()).wav")
                sessionRecordingURL = url
                var initial = wavHeader(dataSize: 0)
                try initial.write(to: url, options: .atomic)
            }
            let nextPCMBytes = sessionRecordingPCMBytes + pending.pcm.count
            guard nextPCMBytes <= Int(UInt32.max) - 36 else { throw VoiceError.recordingTooLarge }
            let file = try FileHandle(forWritingTo: url)
            defer { file.closeFile() }
            file.seekToEndOfFile()
            file.write(pending.pcm)
            file.seek(toFileOffset: 0)
            file.write(wavHeader(dataSize: nextPCMBytes))
            sessionRecordingPCMBytes = nextPCMBytes
            return SessionRecording(
                url: url,
                durationMs: Int64(nextPCMBytes) * 1_000 / (16_000 * 2),
                sizeBytes: nextPCMBytes + 44
            )
        } catch {
            emitError("这段录音暂时无法保存")
            return nil
        }
    }

    private func discardPendingRecording() {
        pendingRecording = nil
    }

    private func resetRecordingSession() {
        sessionRecordingURL = nil
        sessionRecordingPCMBytes = 0
        finishRecordingAfterPending = false
    }

    private func wavHeader(dataSize: Int) -> Data {
        var data = Data()
        func ascii(_ value: String) { data.append(contentsOf: value.utf8) }
        func uint16(_ value: UInt16) {
            var littleEndian = value.littleEndian
            withUnsafeBytes(of: &littleEndian) { data.append(contentsOf: $0) }
        }
        func uint32(_ value: UInt32) {
            var littleEndian = value.littleEndian
            withUnsafeBytes(of: &littleEndian) { data.append(contentsOf: $0) }
        }
        ascii("RIFF")
        uint32(UInt32(36 + dataSize))
        ascii("WAVE")
        ascii("fmt ")
        uint32(16)
        uint16(1)
        uint16(1)
        uint32(16_000)
        uint32(32_000)
        uint16(2)
        uint16(16)
        ascii("data")
        uint32(UInt32(dataSize))
        return data
    }

    private func preparePlayback() {
        playbackQueue.async {
            self.stopPlaybackOnQueue()
            do {
                let session = AVAudioSession.sharedInstance()
                try session.setCategory(.playAndRecord, mode: .voiceChat, options: [.defaultToSpeaker, .allowBluetooth, .duckOthers])
                try session.setActive(true)
                self.playbackEngine.attach(self.playerNode)
                self.playbackEngine.connect(self.playerNode, to: self.playbackEngine.mainMixerNode, format: self.playbackFormat)
                self.playbackEngine.prepare()
                try self.playbackEngine.start()
                self.playerNode.play()
            } catch {
                self.emitError("当前设备无法播放采访语音")
            }
        }
    }

    private func enqueuePlayback(_ data: Data) {
        playbackQueue.async {
            guard data.count >= 2, self.playbackEngine.isRunning else { return }
            let frames = AVAudioFrameCount(data.count / MemoryLayout<Int16>.size)
            guard let buffer = AVAudioPCMBuffer(pcmFormat: self.playbackFormat, frameCapacity: frames),
                  let destination = buffer.int16ChannelData?[0] else { return }
            buffer.frameLength = frames
            data.copyBytes(to: UnsafeMutableRawBufferPointer(start: destination, count: data.count))
            let generation = self.playbackGeneration
            self.pendingPlaybackBuffers += 1
            self.playerNode.scheduleBuffer(buffer) {
                self.playbackQueue.async {
                    guard generation == self.playbackGeneration else { return }
                    self.pendingPlaybackBuffers = max(0, self.pendingPlaybackBuffers - 1)
                    self.finishPlaybackIfReady()
                }
            }
        }
    }

    private func finishPlaybackIfReady() {
        guard serverFinishedPlayback, pendingPlaybackBuffers == 0 else { return }
        stopPlaybackOnQueue()
        emit("playback_finished")
    }

    private func stopPlayback() {
        playbackQueue.async { self.stopPlaybackOnQueue() }
    }

    private func stopPlaybackOnQueue() {
        playbackGeneration += 1
        pendingPlaybackBuffers = 0
        serverFinishedPlayback = false
        playerNode.stop()
        playbackEngine.stop()
        if playbackEngine.attachedNodes.contains(playerNode) {
            playbackEngine.detach(playerNode)
        }
        if !capturing {
            try? AVAudioSession.sharedInstance().setActive(false, options: .notifyOthersOnDeactivation)
        }
    }

    @objc private func audioSessionInterrupted(_ notification: Notification) {
        guard
            let rawValue = notification.userInfo?[AVAudioSessionInterruptionTypeKey] as? UInt,
            AVAudioSession.InterruptionType(rawValue: rawValue) == .began
        else { return }
        stateQueue.async {
            self.stopCapture(commit: false)
            self.stopPlayback()
            self.emitError("语音被电话或其他音频打断，请稍后继续")
        }
    }

    private func sendJSON(_ object: [String: Any]) {
        guard connected, JSONSerialization.isValidJSONObject(object),
              let data = try? JSONSerialization.data(withJSONObject: object),
              let text = String(data: data, encoding: .utf8) else { return }
        socket?.send(.string(text)) { _ in }
    }

    private func emit(_ type: String, _ fields: [String: Any] = [:]) {
        var payload = fields
        payload["type"] = type
        DispatchQueue.main.async { self.eventHandler?(payload as NSDictionary) }
    }

    private func emitError(_ message: String, code: String? = nil) {
        var fields: [String: Any] = ["message": message]
        if let code, !code.isEmpty { fields["code"] = code }
        emit("error", fields)
    }

    private func finishConfigure(_ ok: Bool, _ message: String?) {
        guard let completion = configureCompletion else { return }
        configureCompletion = nil
        complete(completion, ok, message)
    }

    private func complete(_ completion: @escaping (NSDictionary) -> Void, _ ok: Bool, _ message: String?) {
        DispatchQueue.main.async { completion(self.result(ok, message)) }
    }

    private func result(_ ok: Bool, _ message: String?) -> NSDictionary {
        var value: [String: Any] = ["ok": ok]
        if let message, !message.isEmpty { value["message"] = message }
        return value as NSDictionary
    }

    private func validGatewayURL(_ url: URL) -> Bool {
        if url.scheme?.lowercased() == "wss" { return true }
        #if DEBUG
        return url.scheme?.lowercased() == "ws"
        #else
        return false
        #endif
    }

    private func clientInstanceID() -> String {
        if let existing = Self.readKeychain(Self.clientInstanceAccount), !existing.isEmpty { return existing }
        let created = "ios-\(UUID().uuidString.lowercased())"
        Self.writeKeychain(Self.clientInstanceAccount, created)
        return created
    }

    private static func readKeychain(_ account: String) -> String? {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: account,
            kSecReturnData as String: true,
            kSecMatchLimit as String: kSecMatchLimitOne,
        ]
        var item: CFTypeRef?
        guard SecItemCopyMatching(query as CFDictionary, &item) == errSecSuccess,
              let data = item as? Data else { return nil }
        return String(data: data, encoding: .utf8)
    }

    @discardableResult
    private static func writeKeychain(_ account: String, _ value: String) -> Bool {
        deleteKeychain(account)
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: account,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
            kSecValueData as String: Data(value.utf8),
        ]
        return SecItemAdd(query as CFDictionary, nil) == errSecSuccess
    }

    private static func deleteKeychain(_ account: String) {
        let query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: keychainService,
            kSecAttrAccount as String: account,
        ]
        SecItemDelete(query as CFDictionary)
    }
}

private enum VoiceError: Error {
    case audioFormat
    case recordingTooLarge
}

private enum CaptureMode {
    case none
    case monitor
    case listening
}

private struct PendingRecording {
    let pcm: Data
}

private struct SessionRecording {
    let url: URL
    let durationMs: Int64
    let sizeBytes: Int
}
