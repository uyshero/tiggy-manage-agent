package com.tma.biography.voice

import android.Manifest
import android.content.Context
import android.content.pm.ApplicationInfo
import android.content.pm.PackageManager
import android.media.AudioAttributes
import android.media.AudioFocusRequest
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.media.audiofx.AcousticEchoCanceler
import android.media.audiofx.NoiseSuppressor
import android.net.Uri
import android.os.Build
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import androidx.security.crypto.EncryptedSharedPreferences
import androidx.security.crypto.MasterKey
import com.alibaba.fastjson.JSON
import com.alibaba.fastjson.JSONObject
import io.dcloud.feature.uniapp.annotation.UniJSMethod
import io.dcloud.feature.uniapp.bridge.UniJSCallback
import io.dcloud.feature.uniapp.common.UniModule
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import okio.ByteString
import okio.ByteString.Companion.toByteString
import java.io.ByteArrayOutputStream
import java.io.File
import java.io.RandomAccessFile
import java.util.UUID
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean
import kotlin.math.max
import kotlin.math.sqrt

class TMABiographyVoiceModule : UniModule() {
    companion object {
        private const val microphoneRequestCode = 7081
        private const val captureSampleRate = 16_000
        private const val playbackSampleRate = 24_000
        private const val speechGraceMs = 450L
        private const val minimumSpeechMs = 120L
        private const val silenceTimeoutMs = 1_800L
        private const val maxUtteranceMs = 90_000L
        private const val noSpeechTimeoutMs = 30_000L
        private const val voiceThreshold = 330.0
        private const val interruptionGraceMs = 700L
        private const val interruptionMinimumSpeechMs = 180L
        private const val interruptionThreshold = 500.0
        private const val preRollMaxBytes = 48_000
        private const val preferencesName = "tma_biography_voice"
        private const val clientInstanceKey = "client_instance_id"
        private const val resumeTokenKey = "resume_token"
    }

    private val mainHandler = Handler(Looper.getMainLooper())
    private val httpClient = OkHttpClient.Builder()
        .pingInterval(20, TimeUnit.SECONDS)
        .readTimeout(0, TimeUnit.MILLISECONDS)
        .build()
    private val audioExecutor = Executors.newSingleThreadExecutor()
    private val recording = AtomicBoolean(false)
    @Volatile private var captureAudioSent = false
    @Volatile private var captureMode = CaptureMode.NONE
    @Volatile private var manualCommit = false
    @Volatile private var deferInterviewOnNextCommit = false
    @Volatile private var captureStartedAt = 0L
    @Volatile private var lastVoiceAt = 0L
    @Volatile private var speechDetected = false
    @Volatile private var candidateSpeechMs = 0L
    @Volatile private var interruptionDetected = false

    @Volatile private var disposed = false
    @Volatile private var connected = false
    @Volatile private var socket: WebSocket? = null
    @Volatile private var recorder: AudioRecord? = null
    @Volatile private var player: AudioTrack? = null
    private var echoCanceler: AcousticEchoCanceler? = null
    private var noiseSuppressor: NoiseSuppressor? = null
    private val preRoll = ArrayDeque<ByteArray>()
    private var preRollBytes = 0
    private var capturedPCM = ByteArrayOutputStream()
    private var pendingRecording: PendingRecording? = null
    private var sessionRecordingFile: File? = null
    private var sessionRecordingPCMBytes = 0L
    private var finishRecordingAfterPending = false
    private var eventCallback: UniJSCallback? = null
    private var configureCallback: UniJSCallback? = null
    private var gatewayURL = ""
    private var accessToken = ""
    private var reconnectAttempt = 0
    private var currentSessionID = ""
    private var focusRequest: AudioFocusRequest? = null

    private val context: Context
        get() = mUniSDKInstance.context.applicationContext

    private val securePreferences by lazy {
        val masterKey = MasterKey.Builder(context)
            .setKeyScheme(MasterKey.KeyScheme.AES256_GCM)
            .build()
        EncryptedSharedPreferences.create(
            context,
            preferencesName,
            masterKey,
            EncryptedSharedPreferences.PrefKeyEncryptionScheme.AES256_SIV,
            EncryptedSharedPreferences.PrefValueEncryptionScheme.AES256_GCM,
        )
    }

    @UniJSMethod(uiThread = true)
    fun configure(options: JSONObject?, callback: UniJSCallback) {
        val rawURL = options?.getString("gatewayURL")?.trim().orEmpty()
        if (!validGatewayURL(rawURL)) {
            callback.invoke(result(false, "请先配置安全的语音网关地址"))
            return
        }
        disposed = false
        gatewayURL = rawURL
        accessToken = options?.getString("shortLivedToken")?.trim().orEmpty()
        configureCallback = callback
        socket?.cancel()
        socket = null
        connected = false
        connect()
    }

    @UniJSMethod(uiThread = true)
    fun addEventListener(callback: UniJSCallback) {
        eventCallback = callback
    }

    @UniJSMethod(uiThread = true)
    fun removeEventListener() {
        eventCallback = null
    }

    @UniJSMethod(uiThread = true)
    fun startListening(options: JSONObject?, callback: UniJSCallback) {
        if (!connected) {
            callback.invoke(result(false, "语音服务正在重新连接，请稍后再试"))
            return
        }
        if (recording.get() && captureMode == CaptureMode.MONITOR) {
            promoteCapture()
            callback.invoke(result(true))
            return
        }
        if (recording.get()) {
            callback.invoke(result(true))
            return
        }
        if (context.checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            val activity = mUniSDKInstance.context as? android.app.Activity
            if (activity != null) {
                activity.requestPermissions(arrayOf(Manifest.permission.RECORD_AUDIO), microphoneRequestCode)
            }
            callback.invoke(result(false, "请允许麦克风权限后再次点击开始讲述"))
            return
        }
        val sampleRate = options?.getIntValue("sampleRate")?.takeIf { it > 0 } ?: captureSampleRate
        if (sampleRate != captureSampleRate) {
            callback.invoke(result(false, "当前仅支持 16000Hz 录音"))
            return
        }
        manualCommit = options?.getBooleanValue("manualCommit") == true
        stopPlaybackInternal()
        startCapture(CaptureMode.LISTENING, callback)
    }

    @UniJSMethod(uiThread = false)
    fun stopListening(options: JSONObject?, callback: UniJSCallback) {
        deferInterviewOnNextCommit = options?.getBooleanValue("deferInterview") == true
        stopCapture(commit = true)
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = false)
    fun cancelListening(callback: UniJSCallback) {
        stopCapture(commit = false)
        if (connected) {
            sendJSON(JSONObject().apply {
                put("type", "input.cancel")
                put("session_id", currentSessionID)
            })
        }
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = true)
    fun requestFollowup(options: JSONObject?, callback: UniJSCallback) {
        val text = options?.getString("text")?.trim().orEmpty()
        if (!connected || text.isEmpty()) {
            callback.invoke(result(false, if (text.isEmpty()) "没有可提交的采访内容" else "语音服务正在重新连接"))
            return
        }
        sendJSON(JSONObject().apply {
            put("type", "interview.followup")
            put("session_id", currentSessionID)
            put("text", text)
        })
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = true)
    fun setInterviewOrder(options: JSONObject?, callback: UniJSCallback) {
        val order = options?.getString("interviewOrder")?.trim().orEmpty()
        if (!connected) {
            callback.invoke(result(false, "语音服务正在重新连接"))
            return
        }
        if (order !in setOf("chronological", "key_moments", "custom")) {
            callback.invoke(result(false, "采访方式无效"))
            return
        }
        sendJSON(JSONObject().apply {
            put("type", "interview.order.set")
            put("session_id", currentSessionID)
            put("interview_order", order)
        })
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = true)
    fun playText(options: JSONObject?, callback: UniJSCallback) {
        val text = options?.getString("text")?.trim().orEmpty()
        if (!connected || text.isEmpty()) {
            callback.invoke(result(false, if (text.isEmpty()) "没有可播放的采访内容" else "语音服务正在重新连接"))
            return
        }
        stopCapture(commit = false)
        sendJSON(JSONObject().apply {
            put("type", "tts.start")
            put("session_id", currentSessionID)
            put("text", text)
            put("expression", options?.getString("expression")?.trim().orEmpty())
        })
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = true)
    fun cancelPlayback(callback: UniJSCallback) {
        stopPlaybackInternal()
        if (connected) {
            sendJSON(JSONObject().apply {
                put("type", "tts.cancel")
                put("session_id", currentSessionID)
            })
        }
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = false)
    @Synchronized
    fun finishRecordingSession(callback: UniJSCallback) {
        if (pendingRecording == null) resetRecordingSession()
        else finishRecordingAfterPending = true
        callback.invoke(result(true))
    }

    @UniJSMethod(uiThread = false)
    fun deleteRecording(options: JSONObject?, callback: UniJSCallback) {
        val rawPath = options?.getString("filePath")?.trim().orEmpty()
        val recordingDirectory = File(context.filesDir, "biography-recordings")
        val target = try {
            File(Uri.parse(rawPath).path ?: rawPath).canonicalFile
        } catch (_: Exception) {
            null
        }
        val allowed = try {
            target != null && target.path.startsWith(recordingDirectory.canonicalPath + File.separator)
        } catch (_: Exception) {
            false
        }
        if (!allowed || target == null) {
            callback.invoke(result(false, "录音文件地址无效"))
            return
        }
        callback.invoke(result(!target.exists() || target.delete(), if (target.exists()) "录音文件删除失败" else null))
    }

    @UniJSMethod(uiThread = true)
    fun dispose(callback: UniJSCallback) {
        disposed = true
        mainHandler.removeCallbacksAndMessages(null)
        stopCapture(commit = false)
        discardPendingRecording()
        resetRecordingSession()
        stopPlaybackInternal()
        if (connected) {
            sendJSON(JSONObject().apply {
                put("type", "session.finish")
                put("session_id", currentSessionID)
            })
        }
        socket?.close(1000, "page closed")
        socket = null
        connected = false
        configureCallback = null
        callback.invoke(result(true))
    }

    override fun onActivityDestroy() {
        disposed = true
        stopCapture(commit = false)
        discardPendingRecording()
        resetRecordingSession()
        stopPlaybackInternal()
        socket?.cancel()
        socket = null
        connected = false
        super.onActivityDestroy()
    }

    private fun connect() {
        if (disposed || gatewayURL.isEmpty()) return
        val request = try {
            Request.Builder().url(gatewayURL).apply {
                if (accessToken.isNotEmpty()) header("Authorization", "Bearer $accessToken")
            }.build()
        } catch (_: IllegalArgumentException) {
            finishConfigure(false, "语音网关地址无效")
            return
        }
        val opened = httpClient.newWebSocket(request, object : WebSocketListener() {
            override fun onOpen(webSocket: WebSocket, response: Response) {
                if (webSocket !== socket || disposed) {
                    webSocket.close(1000, "stale connection")
                    return
                }
                val wasReconnecting = reconnectAttempt > 0
                connected = true
                reconnectAttempt = 0
                currentSessionID = "voice-${UUID.randomUUID()}"
                startGatewaySession()
                finishConfigure(true, null)
                if (wasReconnecting) emit("network_restored")
            }

            override fun onMessage(webSocket: WebSocket, text: String) {
                if (webSocket === socket) handleServerMessage(text)
            }

            override fun onMessage(webSocket: WebSocket, bytes: ByteString) {
                if (webSocket === socket) enqueuePlayback(bytes.toByteArray())
            }

            override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                webSocket.close(code, reason)
            }

            override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                if (webSocket === socket) handleDisconnect(null)
            }

            override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                if (webSocket === socket) handleDisconnect(t)
            }
        })
        socket = opened
    }

    private fun handleDisconnect(error: Throwable?) {
        val wasConnected = connected
        connected = false
        socket = null
        stopCapture(commit = false)
        discardPendingRecording()
        if (finishRecordingAfterPending) resetRecordingSession()
        stopPlaybackInternal()
        if (disposed) return
        if (configureCallback != null) finishConfigure(false, "无法连接语音服务")
        if (wasConnected) emit("network_lost")
        if (error != null && reconnectAttempt == 0) emitError("语音连接中断，正在重新连接")
        val delay = minOf(30_000L, 1_000L shl minOf(reconnectAttempt, 5))
        reconnectAttempt += 1
        mainHandler.postDelayed({ connect() }, delay)
    }

    private fun startGatewaySession() {
        sendJSON(JSONObject().apply {
            put("type", "session.start")
            put("session_id", currentSessionID)
            put("client_instance_id", clientInstanceID())
            val resumeToken = securePreferences.getString(resumeTokenKey, "").orEmpty()
            if (resumeToken.isNotEmpty()) put("resume_token", resumeToken)
        })
    }

    private fun handleServerMessage(text: String) {
        val message = try {
            JSON.parseObject(text)
        } catch (_: Exception) {
            emitError("语音服务返回了无法识别的数据")
            return
        }
        when (message.getString("type")) {
            "asr.partial" -> emit("partial_transcript", "text" to message.getString("text").orEmpty())
            "asr.final" -> {
                val transcript = message.getString("text").orEmpty()
                emit("final_transcript", "text" to transcript)
                resolvePendingRecording(transcript)
            }
            "interview.project", "interview.project.updated" -> {
                val resumeToken = message.getString("resume_token")?.trim().orEmpty()
                if (resumeToken.isNotEmpty()) securePreferences.edit().putString(resumeTokenKey, resumeToken).apply()
                message.getJSONObject("project")?.let { emit("project_loaded", "project" to it) }
            }
            "interview.reply" -> {
                val resumeToken = message.getString("resume_token")?.trim().orEmpty()
                if (resumeToken.isNotEmpty()) securePreferences.edit().putString(resumeTokenKey, resumeToken).apply()
                emit(
                    "assistant_reply",
                    "text" to message.getString("text").orEmpty(),
                    "expression" to message.getString("expression").orEmpty(),
                    "project" to message.getJSONObject("project"),
                )
            }
            "tts.started" -> {
                preparePlayback()
                emit("playback_started")
            }
            "tts.finished" -> finishPlaybackWhenDrained()
            "tts.canceled" -> stopPlaybackInternal()
            "error" -> {
                discardPendingRecording()
                if (finishRecordingAfterPending) resetRecordingSession()
                val code = message.getString("code")
                if (code == "resume_invalid") {
                    securePreferences.edit().remove(resumeTokenKey).apply()
                    startGatewaySession()
                }
                emitError(message.getString("message") ?: "语音服务暂时不可用", code)
            }
        }
    }

    @Suppress("MissingPermission")
    private fun startCapture(mode: CaptureMode, callback: UniJSCallback?) {
        val minimum = AudioRecord.getMinBufferSize(
            captureSampleRate,
            AudioFormat.CHANNEL_IN_MONO,
            AudioFormat.ENCODING_PCM_16BIT,
        )
        if (minimum <= 0) {
            callback?.invoke(result(false, "当前设备无法初始化麦克风"))
            return
        }
        val audioRecord = try {
            AudioRecord(
                MediaRecorder.AudioSource.VOICE_RECOGNITION,
                captureSampleRate,
                AudioFormat.CHANNEL_IN_MONO,
                AudioFormat.ENCODING_PCM_16BIT,
                max(minimum, 3_200),
            )
        } catch (_: Exception) {
            callback?.invoke(result(false, "当前设备无法打开麦克风"))
            return
        }
        if (audioRecord.state != AudioRecord.STATE_INITIALIZED) {
            audioRecord.release()
            callback?.invoke(result(false, "当前设备无法打开麦克风"))
            return
        }
        try {
            if (AcousticEchoCanceler.isAvailable()) {
                echoCanceler = AcousticEchoCanceler.create(audioRecord.audioSessionId)?.apply { enabled = true }
            }
            if (NoiseSuppressor.isAvailable()) {
                noiseSuppressor = NoiseSuppressor.create(audioRecord.audioSessionId)?.apply { enabled = true }
            }
        } catch (_: RuntimeException) {
            echoCanceler?.release()
            echoCanceler = null
            noiseSuppressor?.release()
            noiseSuppressor = null
        }
        recorder = audioRecord
        captureMode = mode
        captureAudioSent = false
        captureStartedAt = SystemClock.elapsedRealtime()
        lastVoiceAt = captureStartedAt
        speechDetected = false
        candidateSpeechMs = 0L
        interruptionDetected = false
        clearPreRoll()
        capturedPCM = ByteArrayOutputStream()
        try {
            audioRecord.startRecording()
        } catch (_: IllegalStateException) {
            recorder = null
            captureMode = CaptureMode.NONE
            echoCanceler?.release()
            echoCanceler = null
            noiseSuppressor?.release()
            noiseSuppressor = null
            audioRecord.release()
            callback?.invoke(result(false, "当前设备无法打开麦克风"))
            return
        }
        recording.set(true)
        callback?.invoke(result(true))
        Thread({ captureLoop(audioRecord) }, "tma-biography-capture").start()
    }

    private fun captureLoop(audioRecord: AudioRecord) {
        val samples = ShortArray(1_600)
        while (recording.get() && recorder === audioRecord) {
            val count = audioRecord.read(samples, 0, samples.size)
            if (count <= 0) continue
            val bytes = ByteArray(count * 2)
            var energy = 0.0
            for (index in 0 until count) {
                val sample = samples[index].toInt()
                energy += sample.toDouble() * sample.toDouble()
                bytes[index * 2] = (sample and 0xff).toByte()
                bytes[index * 2 + 1] = ((sample ushr 8) and 0xff).toByte()
            }
            val now = SystemClock.elapsedRealtime()
            synchronized(this) {
                if (!recording.get() || recorder !== audioRecord) return
                val activeMode = captureMode
                val grace = if (activeMode == CaptureMode.MONITOR) interruptionGraceMs else speechGraceMs
                if (now - captureStartedAt < grace) return@synchronized
                if (activeMode == CaptureMode.LISTENING) {
                    socket?.takeIf { connected }?.send(bytes.toByteString())
                    captureAudioSent = true
                    capturedPCM.write(bytes)
                } else if (activeMode == CaptureMode.MONITOR) {
                    appendPreRoll(bytes)
                }
                val threshold = if (activeMode == CaptureMode.MONITOR) interruptionThreshold else voiceThreshold
                val requiredSpeechMs = if (activeMode == CaptureMode.MONITOR) interruptionMinimumSpeechMs else minimumSpeechMs
                if (sqrt(energy / count) >= threshold) {
                    candidateSpeechMs += count.toLong() * 1_000L / captureSampleRate
                    lastVoiceAt = now
                    if (!speechDetected && candidateSpeechMs >= requiredSpeechMs) {
                        speechDetected = true
                        if (activeMode == CaptureMode.MONITOR) interruptionDetected = true
                        emit("speech_detected")
                    }
                } else if (!speechDetected) {
                    candidateSpeechMs = 0
                }
                if (activeMode == CaptureMode.LISTENING && !manualCommit) {
                    val endedBySilence = speechDetected && now - lastVoiceAt >= silenceTimeoutMs
                    val noSpeechTimedOut = !speechDetected && now - captureStartedAt >= noSpeechTimeoutMs
                    if (endedBySilence || noSpeechTimedOut || now - captureStartedAt >= maxUtteranceMs) {
                        stopCapture(commit = true)
                        return
                    }
                } else if (activeMode == CaptureMode.LISTENING && now - captureStartedAt >= maxUtteranceMs) {
                    stopCapture(commit = true)
                    return
                }
            }
        }
    }

    @Synchronized
    private fun promoteCapture() {
        if (!recording.get() || captureMode != CaptureMode.MONITOR) return
        captureMode = CaptureMode.LISTENING
        captureStartedAt = SystemClock.elapsedRealtime()
        if (interruptionDetected) {
            preRoll.forEach { audio ->
                socket?.takeIf { connected }?.send(audio.toByteString())
                capturedPCM.write(audio)
            }
            captureAudioSent = preRollBytes > 0
        } else {
            speechDetected = false
            candidateSpeechMs = 0L
            lastVoiceAt = captureStartedAt
            capturedPCM = ByteArrayOutputStream()
        }
        clearPreRoll()
    }

    private fun appendPreRoll(audio: ByteArray) {
        preRoll.addLast(audio)
        preRollBytes += audio.size
        while (preRollBytes > preRollMaxBytes && preRoll.size > 1) {
            preRollBytes -= preRoll.removeFirst().size
        }
    }

    private fun clearPreRoll() {
        preRoll.clear()
        preRollBytes = 0
    }

    @Synchronized
    private fun stopCapture(commit: Boolean) {
        if (!recording.getAndSet(false)) return
        val active = recorder
        recorder = null
        try { active?.stop() } catch (_: IllegalStateException) {}
        active?.release()
        echoCanceler?.release()
        echoCanceler = null
        noiseSuppressor?.release()
        noiseSuppressor = null
        val shouldCommit = commit && captureMode == CaptureMode.LISTENING && connected && captureAudioSent
        val deferInterview = deferInterviewOnNextCommit
        if (shouldCommit && capturedPCM.size() > 0) {
            pendingRecording = writeRecording(capturedPCM.toByteArray())
        }
        captureAudioSent = false
        captureMode = CaptureMode.NONE
        manualCommit = false
        deferInterviewOnNextCommit = false
        interruptionDetected = false
        clearPreRoll()
        capturedPCM = ByteArrayOutputStream()
        if (shouldCommit) {
            sendJSON(JSONObject().apply {
                put("type", "input.commit")
                put("session_id", currentSessionID)
                put("defer_interview", deferInterview)
            })
        }
    }

    private fun writeRecording(pcm: ByteArray): PendingRecording? {
        if (pcm.isEmpty()) return null
        return PendingRecording(pcm)
    }

    @Synchronized
    private fun resolvePendingRecording(transcript: String) {
        val pending = pendingRecording
        pendingRecording = null
        if (pending != null && transcript.isNotBlank()) {
            appendToSessionRecording(pending)?.let { recording ->
                emit(
                    "recording_ready",
                    "filePath" to Uri.fromFile(recording.file).toString(),
                    "durationMs" to recording.durationMs,
                    "sizeBytes" to recording.sizeBytes,
                    "transcript" to transcript.trim(),
                    "cumulative" to true,
                )
            }
        }
        if (finishRecordingAfterPending) resetRecordingSession()
    }

    private fun appendToSessionRecording(pending: PendingRecording): SessionRecording? {
        return try {
            val file = sessionRecordingFile ?: run {
                val directory = File(context.filesDir, "biography-recordings").apply { mkdirs() }
                File(directory, "interview-${System.currentTimeMillis()}-${UUID.randomUUID()}.wav").also {
                    sessionRecordingFile = it
                }
            }
            val nextPCMBytes = sessionRecordingPCMBytes + pending.pcm.size
            require(nextPCMBytes <= Int.MAX_VALUE.toLong() - 36L) { "recording is too large" }
            RandomAccessFile(file, "rw").use { output ->
                if (sessionRecordingPCMBytes == 0L) {
                    output.setLength(0)
                    output.write(wavHeader(0))
                }
                output.seek(output.length())
                output.write(pending.pcm)
                output.seek(0)
                output.write(wavHeader(nextPCMBytes.toInt()))
            }
            sessionRecordingPCMBytes = nextPCMBytes
            SessionRecording(
                file,
                nextPCMBytes * 1_000L / (captureSampleRate * 2L),
                file.length(),
            )
        } catch (_: Exception) {
            emitError("这段录音暂时无法保存")
            null
        }
    }

    private fun discardPendingRecording() {
        pendingRecording = null
    }

    private fun resetRecordingSession() {
        sessionRecordingFile = null
        sessionRecordingPCMBytes = 0L
        finishRecordingAfterPending = false
    }

    private fun wavHeader(dataSize: Int): ByteArray {
        val header = ByteArray(44)
        fun ascii(offset: Int, value: String) = value.forEachIndexed { index, char -> header[offset + index] = char.code.toByte() }
        fun shortLE(offset: Int, value: Int) {
            header[offset] = (value and 0xff).toByte()
            header[offset + 1] = ((value ushr 8) and 0xff).toByte()
        }
        fun intLE(offset: Int, value: Int) {
            for (index in 0..3) header[offset + index] = ((value ushr (index * 8)) and 0xff).toByte()
        }
        ascii(0, "RIFF")
        intLE(4, 36 + dataSize)
        ascii(8, "WAVE")
        ascii(12, "fmt ")
        intLE(16, 16)
        shortLE(20, 1)
        shortLE(22, 1)
        intLE(24, captureSampleRate)
        intLE(28, captureSampleRate * 2)
        shortLE(32, 2)
        shortLE(34, 16)
        ascii(36, "data")
        intLE(40, dataSize)
        return header
    }

    private fun preparePlayback() {
        audioExecutor.execute {
            stopPlaybackInternal()
            requestAudioFocus()
            val minimum = AudioTrack.getMinBufferSize(
                playbackSampleRate,
                AudioFormat.CHANNEL_OUT_MONO,
                AudioFormat.ENCODING_PCM_16BIT,
            )
            val track = AudioTrack.Builder()
                .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_ASSISTANCE_ACCESSIBILITY).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
                .setAudioFormat(AudioFormat.Builder().setSampleRate(playbackSampleRate).setChannelMask(AudioFormat.CHANNEL_OUT_MONO).setEncoding(AudioFormat.ENCODING_PCM_16BIT).build())
                .setBufferSizeInBytes(max(minimum, 9_600))
                .setTransferMode(AudioTrack.MODE_STREAM)
                .build()
            player = track
            track.play()
        }
    }

    private fun enqueuePlayback(audio: ByteArray) {
        audioExecutor.execute {
            synchronized(this) {
                val track = player ?: return@synchronized
                track.write(audio, 0, audio.size, AudioTrack.WRITE_BLOCKING)
            }
        }
    }

    private fun finishPlaybackWhenDrained() {
        audioExecutor.execute {
            val track = player
            if (track != null) {
                try { track.stop() } catch (_: IllegalStateException) {}
                track.release()
                if (player === track) player = null
            }
            abandonAudioFocus()
            emit("playback_finished")
        }
    }

    @Synchronized
    private fun stopPlaybackInternal() {
        val track = player
        player = null
        if (track != null) {
            try {
                track.pause()
                track.flush()
                track.stop()
            } catch (_: IllegalStateException) {}
            track.release()
        }
        abandonAudioFocus()
    }

    private fun requestAudioFocus() {
        val manager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            focusRequest = AudioFocusRequest.Builder(AudioManager.AUDIOFOCUS_GAIN_TRANSIENT_MAY_DUCK)
                .setAudioAttributes(AudioAttributes.Builder().setUsage(AudioAttributes.USAGE_ASSISTANCE_ACCESSIBILITY).setContentType(AudioAttributes.CONTENT_TYPE_SPEECH).build())
                .setOnAudioFocusChangeListener { if (it == AudioManager.AUDIOFOCUS_LOSS) stopPlaybackInternal() }
                .build()
            manager.requestAudioFocus(focusRequest!!)
        } else {
            @Suppress("DEPRECATION")
            manager.requestAudioFocus(null, AudioManager.STREAM_MUSIC, AudioManager.AUDIOFOCUS_GAIN_TRANSIENT_MAY_DUCK)
        }
    }

    private fun abandonAudioFocus() {
        val manager = context.getSystemService(Context.AUDIO_SERVICE) as AudioManager
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.O) {
            focusRequest?.let { manager.abandonAudioFocusRequest(it) }
            focusRequest = null
        } else {
            @Suppress("DEPRECATION")
            manager.abandonAudioFocus(null)
        }
    }

    private fun sendJSON(message: JSONObject) {
        socket?.takeIf { connected }?.send(message.toJSONString())
    }

    private fun emit(type: String, vararg fields: Pair<String, Any?>) {
        val payload = JSONObject()
        payload["type"] = type
        fields.forEach { (key, value) -> if (value != null) payload[key] = value }
        mainHandler.post { eventCallback?.invokeAndKeepAlive(payload) }
    }

    private fun emitError(message: String, code: String? = null) = emit("error", "message" to message, "code" to code)

    private fun finishConfigure(ok: Boolean, message: String?) {
        val callback = configureCallback ?: return
        configureCallback = null
        mainHandler.post { callback.invoke(result(ok, message)) }
    }

    private fun result(ok: Boolean, message: String? = null) = JSONObject().apply {
        put("ok", ok)
        if (!message.isNullOrEmpty()) put("message", message)
    }

    private fun clientInstanceID(): String {
        val existing = securePreferences.getString(clientInstanceKey, "")?.trim().orEmpty()
        if (existing.isNotEmpty()) return existing
        val created = "android-${UUID.randomUUID()}"
        securePreferences.edit().putString(clientInstanceKey, created).commit()
        return created
    }

    private fun validGatewayURL(value: String): Boolean {
        val scheme = try { Uri.parse(value).scheme?.lowercase() } catch (_: Exception) { null }
        if (scheme == "wss") return true
        val debugBuild = context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0
        return debugBuild && scheme == "ws"
    }

    private enum class CaptureMode { NONE, MONITOR, LISTENING }

    private data class PendingRecording(val pcm: ByteArray)
    private data class SessionRecording(val file: File, val durationMs: Long, val sizeBytes: Long)
}
