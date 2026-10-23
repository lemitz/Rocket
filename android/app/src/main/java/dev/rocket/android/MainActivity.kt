package dev.rocket.android

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.view.WindowManager
import android.widget.Button
import android.widget.EditText
import android.widget.LinearLayout
import android.widget.ScrollView
import android.widget.TextView
import java.io.IOException
import java.net.ConnectException
import java.net.DatagramPacket
import java.net.DatagramSocket
import java.net.Inet4Address
import java.net.InetAddress
import java.net.NetworkInterface
import java.net.Socket
import java.net.SocketException
import java.net.SocketTimeoutException
import java.nio.charset.StandardCharsets
import java.security.SecureRandom
import org.json.JSONException

internal class TransferTask(private val cancelledMessage: String) {
    @Volatile private var cancelled = false
    private var socket: Socket? = null
    private var udp: DatagramSocket? = null

    @Synchronized fun register(value: Socket) {
        if (cancelled) { value.close(); throw IOException(cancelledMessage) }
        socket = value
    }
    @Synchronized fun register(value: DatagramSocket) {
        if (cancelled) { value.close(); throw IOException(cancelledMessage) }
        udp = value
    }
    @Synchronized fun cancel() {
        cancelled = true
        socket?.close()
        udp?.close()
    }
    fun check() { if (cancelled) throw IOException(cancelledMessage) }
    fun isCancelled() = cancelled
}

class MainActivity : Activity() {
    private val main = Handler(Looper.getMainLooper())
    private val prefs by lazy { getSharedPreferences("rocket", MODE_PRIVATE) }
    private lateinit var pcLabel: TextView
    private lateinit var status: TextView
    private lateinit var selectionLabel: TextView
    private lateinit var scene: LaunchView
    private lateinit var progressText: TextView
    private lateinit var launchButton: Button
    private lateinit var cancelButton: Button
    private val controls = mutableListOf<Button>()
    private val selected = mutableListOf<Uri>()
    private var uploads: List<UploadFile> = emptyList()
    private var selectionVersion = 0
    private var active: TransferTask? = null
    private var polling: TransferTask? = null
    private var resumed = false
    private var offeredSelection = false
    private var mismatchedPc = false
    private val pollTick = Runnable { pollOnce() }
    private val deviceId by lazy {
        val existing = prefs.getString("device_id", null)
        if (existing != null && validNodeId(existing)) existing else {
            val bytes = ByteArray(16).also { SecureRandom().nextBytes(it) }
            val id = bytes.joinToString("") { "%02x".format(it.toInt() and 0xff) }
            prefs.edit().putString("device_id", id).commit()
            id
        }
    }
    private val deviceName by lazy {
        val name = (Build.MODEL ?: "").filter { !it.isISOControl() && it != '|' }.trim()
        val limited = StringBuilder()
        for (character in name) {
            if ((limited.toString() + character).toByteArray(StandardCharsets.UTF_8).size > 255) break
            limited.append(character)
        }
        limited.toString().trim().ifBlank { getString(R.string.phone_fallback) }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        // Older builds stored typed addresses before verification. Do not silently poll one.
        val savedIp = prefs.getString("ip", "").orEmpty()
        if (savedIp.isNotBlank() && savedIp != prefs.getString("last_ip", null))
            prefs.edit().putString("unverified_ip", savedIp).remove("ip").apply()
        val d = resources.displayMetrics.density
        fun dp(value: Int) = (value * d).toInt()
        val root = ScrollView(this).apply {
            isFillViewport = true
            background = GradientDrawable(GradientDrawable.Orientation.TOP_BOTTOM,
                intArrayOf(0xff061532.toInt(), 0xff12345d.toInt(), 0xff0b274e.toInt()))
            clipToPadding = false
            val pad = dp(16)
            setPadding(pad, pad, pad, pad)
            setOnApplyWindowInsetsListener { view, insets ->
                val bars = insets.getInsets(WindowInsets.Type.systemBars())
                view.setPadding(pad + bars.left, pad + bars.top, pad + bars.right, pad + bars.bottom)
                insets
            }
        }
        setContentView(root)
        window.insetsController?.setSystemBarsAppearance(0,
            WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS)
        val content = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
        root.addView(content)
        fun label(value: String, size: Float, color: Int) = TextView(this).apply {
            text = value
            textSize = size
            setTextColor(color)
            setPadding(dp(4), dp(5), dp(4), dp(5))
        }
        fun button(title: String, action: () -> Unit): Button = Button(this).apply {
            text = title
            textSize = 15f
            isAllCaps = false
            setTextColor(Color.WHITE)
            background = GradientDrawable(GradientDrawable.Orientation.TOP_BOTTOM,
                intArrayOf(0xff3c9ede.toInt(), 0xff1764a5.toInt())).apply { cornerRadius = dp(12).toFloat() }
            setOnClickListener { action() }
            controls.add(this)
        }
        content.addView(label(getString(R.string.header), 20f, 0xfff0f8ff.toInt()))
        content.addView(label(getString(R.string.network_warning), 12f, 0xffa9cbe8.toInt()))
        scene = LaunchView(this)
        content.addView(scene, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(280)))
        status = label(getString(R.string.ready), 16f, 0xfff0f8ff.toInt())
        content.addView(status)
        progressText = label(getString(R.string.choose_or_download), 13f, 0xffb5d4ed.toInt())
        content.addView(progressText)
        selectionLabel = label("", 16f, 0xfff0f8ff.toInt())
        content.addView(selectionLabel)
        content.addView(button(getString(R.string.choose_files)) { chooseFiles() },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(52)))
        launchButton = button(getString(R.string.launch)) { sendSelected() }.apply { textSize = 22f }
        content.addView(launchButton, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(68)).apply {
            topMargin = dp(8)
        })
        pcLabel = label("", 13f, 0xffb5d4ed.toInt())
        content.addView(pcLabel)
        content.addView(button(getString(R.string.pc_settings)) { showSettings() },
            LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(48)))
        cancelButton = button(getString(R.string.cancel)) {
            active?.cancel()
            status.text = getString(R.string.cancelling)
        }.also { controls.remove(it); it.visibility = View.GONE }
        content.addView(cancelButton, LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, dp(48)))
        showPc()
        showSelection()
        handleShare(intent)
    }

    private fun showSelection() {
        selectionLabel.text = if (uploads.isEmpty()) getString(R.string.no_files_selected)
            else getString(R.string.selected_files, fileCount(uploads.size),
                readableBytes(uploads.sumOf { it.size }))
        launchButton.isEnabled = uploads.isNotEmpty() && active == null
    }

    private fun stageFiles(uris: List<Uri>) {
        if (active != null) { status.text = getString(R.string.finish_before_share); return }
        selectionVersion++
        val version = selectionVersion
        selected.clear()
        selected.addAll(uris.distinct())
        uploads = emptyList()
        showSelection()
        if (selected.isEmpty()) return
        status.text = getString(R.string.reading_selection)
        val snapshot = selected.toList()
        Thread {
            var files: List<UploadFile>? = null
            var failure: Exception? = null
            try { files = snapshot.map { uploadFile(this, it) } }
            catch (e: Exception) { failure = e }
            main.post {
                if (selectionVersion != version || isFinishing || isDestroyed) return@post
                if (failure != null) {
                    if (active == null) status.text = getString(R.string.selection_error, userFacingError(failure))
                } else {
                    uploads = checkNotNull(files)
                    if (active == null) status.text = getString(R.string.ready_to_launch)
                }
                showSelection()
            }
        }.start()
    }

    private fun showPc() {
        val ip = prefs.getString("ip", "").orEmpty()
        pcLabel.text = if (mismatchedPc) getString(R.string.pc_reselect)
        else if (ip.isBlank()) getString(R.string.enter_or_find)
        else getString(R.string.selected_pc_compact,
            prefs.getString("last_name", null)?.takeIf { prefs.getString("last_ip", null) == ip }
                ?: getString(R.string.pc_fallback), ip)
    }

    private fun usePc(pc: DiscoveredPc, info: PcInfo) {
        // Explicit selection only; discovery alone never changes the destination.
        prefs.edit().putString("ip", pc.ip).putString("last_ip", pc.ip)
            .putString("last_id", info.id).putString("last_name", info.name)
            .remove("unverified_ip").apply()
        polling?.cancel()
        polling = null
        mismatchedPc = false
        showPc()
        schedulePoll(0)
    }

    private fun showSettings() {
        val d = resources.displayMetrics.density
        fun dp(value: Int) = (value * d).toInt()
        val layout = LinearLayout(this).apply {
            orientation = LinearLayout.VERTICAL
            setPadding(dp(20), dp(8), dp(20), dp(8))
        }
        fun entry(text: String, action: () -> Unit) {
            layout.addView(Button(this).apply {
                this.text = text
                isAllCaps = false
                setOnClickListener { action() }
            })
        }
        layout.addView(TextView(this).apply { text = pcLabel.text; textSize = 16f })
        val dialog = AlertDialog.Builder(this).setTitle(R.string.pc_settings).setView(layout)
            .setNegativeButton(android.R.string.cancel, null).create()
        entry(getString(R.string.find_pc)) { dialog.dismiss(); discover() }
        entry(getString(R.string.enter_ipv4_manually)) { dialog.dismiss(); enterIp() }
        layout.addView(TextView(this).apply {
            text = getString(if (prefs.contains("tree")) R.string.destination_folder else R.string.destination_downloads)
            textSize = 14f
        })
        entry(getString(R.string.folder)) { dialog.dismiss(); chooseFolder() }
        entry(getString(R.string.use_downloads)) {
            clearFolder()
            dialog.dismiss()
            status.text = getString(R.string.destination_downloads)
        }
        dialog.show()
    }

    private fun enterIp() {
        val edit = EditText(this).apply {
            hint = getString(R.string.ip_hint)
            setSingleLine(true)
            inputType = android.text.InputType.TYPE_CLASS_PHONE
            setText(prefs.getString("unverified_ip", null) ?: prefs.getString("ip", ""))
        }
        val d = (resources.displayMetrics.density * 20).toInt()
        val holder = LinearLayout(this).apply { setPadding(d, 0, d, 0); addView(edit) }
        AlertDialog.Builder(this).setTitle(R.string.enter_ipv4_manually).setView(holder)
            .setPositiveButton(R.string.select_pc) { _, _ ->
                val address = edit.text.toString().trim()
                try {
                    pcAddress(this, address)
                    selectPc(DiscoveredPc(address, null, null))
                } catch (e: Exception) { status.text = userFacingError(e) }
            }.setNegativeButton(android.R.string.cancel, null).show()
    }

    private fun clearFolder() {
        prefs.getString("tree", null)?.let { old ->
            try {
                contentResolver.releasePersistableUriPermission(Uri.parse(old),
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
            } catch (_: Exception) { /* grant may already have been revoked */ }
        }
        prefs.edit().remove("tree").apply()
    }

    private fun userFacingError(error: Exception): String = when (error) {
        is RocketError -> error.message ?: getString(R.string.unknown_error)
        is SocketTimeoutException -> getString(R.string.error_timeout)
        is ConnectException -> getString(R.string.error_connect)
        is SocketException -> getString(R.string.error_connection_lost)
        is SecurityException -> getString(R.string.error_file_access)
        is IOException -> getString(R.string.error_io)
        else -> getString(R.string.unknown_error)
    }

    private fun runTask(label: String, onSuccess: (() -> Unit)? = null, transferCount: Int? = null,
                        existingTask: TransferTask? = null,
                        operation: (TransferTask, TransferProgress?) -> String) {
        if (active != null) { status.text = getString(R.string.wait_or_cancel); return }
        if (existingTask == null) { polling?.cancel(); polling = null }
        main.removeCallbacks(pollTick)
        val task = existingTask ?: TransferTask(getString(R.string.error_cancelled))
        active = task
        scene.reset()
        cancelButton.visibility = View.VISIBLE
        cancelButton.isEnabled = true
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        controls.forEach { it.isEnabled = false }
        status.text = getString(R.string.task_running, label)
        progressText.text = getString(if (transferCount != null) R.string.connecting else R.string.please_wait)
        if (transferCount != null) scene.launch()
        val started = SystemClock.elapsedRealtimeNanos()
        var latest = transferCount?.let { TransferSnapshot(0, null, 0, it, 0) }
        val progress = transferCount?.let { count ->
            TransferProgress(count) { snapshot -> main.post {
                if (active === task && !task.isCancelled()) latest = snapshot
            } }
        }
        if (transferCount != null) {
            val ticker = object : Runnable {
                override fun run() {
                    if (active !== task || task.isCancelled()) return
                    latest?.copy(elapsedNanos = (SystemClock.elapsedRealtimeNanos() - started).coerceAtLeast(0))?.let { snapshot ->
                        val total = snapshot.total?.let { getString(R.string.progress_total, readableBytes(it)) } ?: ""
                        val fraction = if (snapshot.total != null && snapshot.total > 0)
                            getString(R.string.progress_percent,
                                (snapshot.bytes.toDouble() / snapshot.total * 100).toInt().coerceIn(0, 100)) else ""
                        progressText.text = getString(R.string.progress_line, snapshot.completed, snapshot.fileCount,
                            readableBytes(snapshot.bytes), total, fraction, readableTime(snapshot.elapsedNanos),
                            readableSpeed(snapshot.bytesPerSecond.toLong()))
                        if (snapshot.completed < snapshot.fileCount && snapshot.total != null && snapshot.bytes >= snapshot.total)
                            status.text = getString(R.string.awaiting_confirmation, label)
                    }
                    main.postDelayed(this, 100)
                }
            }
            main.postDelayed(ticker, 100)
        }
        Thread {
            var result: String? = null
            var failure: Exception? = null
            try { result = operation(task, progress); task.check() }
            catch (e: Exception) {
                failure = e
                if (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0 && !task.isCancelled())
                    Log.w("RocketTransfer", "Operation failed (raw diagnostics)", e)
            }
            val snapshot = if (failure == null && !task.isCancelled()) progress?.finish() else null
            main.post {
                if (active === task) {
                    active = null
                    window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                    controls.forEach { it.isEnabled = true }
                    cancelButton.isEnabled = false
                    cancelButton.visibility = View.GONE
                    showSelection()
                    if (task.isCancelled() || failure != null) {
                        scene.reset()
                        status.text = if (task.isCancelled()) getString(R.string.cancelled)
                            else getString(R.string.task_error, label, userFacingError(checkNotNull(failure)))
                        progressText.text = getString(if (transferCount != null) R.string.no_stats else R.string.operation_failed)
                    } else {
                        status.text = getString(R.string.task_result, label, result)
                        if (snapshot != null) {
                            scene.succeed(snapshot)
                            progressText.text = getString(R.string.confirmed, fileCount(snapshot.completed))
                        } else progressText.text = getString(R.string.done)
                        onSuccess?.invoke()
                    }
                    schedulePoll()
                }
            }
        }.start()
    }

    private fun runTransfer(label: String, count: Int, existingTask: TransferTask? = null,
                            onSuccess: (() -> Unit)? = null,
                            operation: (TransferTask, TransferProgress) -> String) {
        runTask(label, onSuccess, count, existingTask) { task, progress ->
            operation(task, checkNotNull(progress))
        }
    }

    private fun sendSelected() {
        if (active != null) { status.text = getString(R.string.wait_or_cancel); return }
        val files = uploads.toList()
        if (files.isEmpty()) { status.text = getString(R.string.select_file_first); return }
        val address = prefs.getString("ip", "").orEmpty()
        if (address.isBlank() || mismatchedPc) {
            status.text = getString(if (mismatchedPc) R.string.pc_id_mismatch else R.string.enter_or_find)
            showSettings()
            return
        }
        runTransfer(getString(R.string.sending), files.size, onSuccess = {
            selectionVersion++
            selected.clear()
            uploads = emptyList()
            showSelection()
        }) { task, progress ->
            progress.setTotal(files.map { it.size })
            val saved = mutableListOf<String>()
            RocketProtocol(this, address, task).use { client ->
                val expectedId = prefs.getString("last_id", null)
                if (expectedId != null && client.ping().id != expectedId)
                    throw RocketError(getString(R.string.pc_id_mismatch))
                for (file in files) {
                    task.check()
                    saved += client.put(contentResolver, file, progress::transferred)
                    progress.confirmed()
                }
            }
            getString(R.string.files_sent, saved.size, saved.joinToString())
        }
    }

    private fun pollOnce() {
        if (!resumed || active != null || polling != null) return
        val address = prefs.getString("ip", "").orEmpty()
        if (address.isBlank()) return
        val task = TransferTask(getString(R.string.error_cancelled))
        polling = task
        Thread {
            var client: RocketProtocol? = null
            var offers: List<Offer> = emptyList()
            var invalidResponse = false
            var identityMismatch = false
            try {
                client = RocketProtocol(this, address, task)
                val expectedId = prefs.getString("last_id", null)
                if (expectedId != null && client.ping().id != expectedId)
                    throw RocketError(getString(R.string.pc_id_mismatch))
                offers = client.poll(deviceId, deviceName)
            } catch (e: Exception) {
                // Network failures are expected during foreground polling, never prompt for them.
                identityMismatch = e is RocketError && e.message == getString(R.string.pc_id_mismatch)
                invalidResponse = !identityMismatch && (e is RocketError || e is JSONException)
            }
            main.post {
                if (identityMismatch && polling === task && resumed && !task.isCancelled()) {
                    mismatchedPc = true
                    showPc()
                    status.text = getString(R.string.pc_id_mismatch)
                } else if (invalidResponse && polling === task && resumed && !task.isCancelled()) {
                    status.text = getString(R.string.offer_poll_error)
                }
                if (polling !== task || !resumed || active != null ||
                    prefs.getString("ip", "") != address || task.isCancelled()) {
                    client?.close()
                    if (polling === task) { polling = null; schedulePoll() }
                } else if (offers.isEmpty()) {
                    client?.close()
                    polling = null
                    schedulePoll()
                } else {
                    polling = null
                    val connection = checkNotNull(client)
                    receiveOffers(task, connection, offers)
                }
            }
        }.start()
    }

    private fun receiveOffers(task: TransferTask, client: RocketProtocol, offers: List<Offer>) {
        val tree = prefs.getString("tree", null)?.let(Uri::parse)
        runTransfer(getString(R.string.receiving_offers), offers.size, existingTask = task) { _, progress ->
            client.use { connection ->
                val sizes = offers.map { it.size }.toMutableList()
                progress.setTotal(sizes)
                val store = DownloadStore(this, tree)
                val saved = mutableListOf<String>()
                for ((index, offer) in offers.withIndex()) {
                    task.check()
                    val name = connection.offerGet(deviceId, offer, store,
                        { size -> sizes[index] = size; progress.setTotal(sizes) }, progress::transferred)
                    task.check()
                    // Only acknowledge after CRC32C, stream close and publication succeeded.
                    connection.offerDone(deviceId, offer.id)
                    saved += name
                    progress.confirmed()
                }
                getString(R.string.files_received, saved.size, saved.joinToString())
            }
        }
    }

    private fun schedulePoll(delay: Long = 1000) {
        main.removeCallbacks(pollTick)
        if (resumed && active == null && polling == null && !mismatchedPc &&
            !prefs.getString("ip", "").isNullOrBlank())
            main.postDelayed(pollTick, delay)
    }

    private fun chooseFiles() {
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
            putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        }, PICK_FILES)
    }

    private fun chooseFolder() {
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
        }, PICK_FOLDER)
    }

    @Deprecated("Activity result API works without AndroidX")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (resultCode != RESULT_OK || data == null) return
        if (requestCode == PICK_FILES) {
            val uris = mutableListOf<Uri>()
            data.clipData?.let { clips ->
                for (i in 0 until clips.itemCount) uris.add(clips.getItemAt(i).uri)
            } ?: data.data?.let(uris::add)
            stageFiles(uris) // Picking never starts a transfer.
        } else if (requestCode == PICK_FOLDER) {
            val uri = data.data ?: return
            try {
                contentResolver.takePersistableUriPermission(uri,
                    Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                if (prefs.getString("tree", null) != uri.toString()) clearFolder()
                prefs.edit().putString("tree", uri.toString()).apply()
                status.text = getString(R.string.folder_selected)
            } catch (_: SecurityException) {
                status.text = getString(R.string.folder_access_error)
            }
        }
    }

    private fun handleShare(intent: Intent?) {
        if (intent == null) return
        val shared = when (intent.action) {
            Intent.ACTION_SEND -> listOfNotNull(intent.getParcelableExtra(Intent.EXTRA_STREAM, Uri::class.java))
            Intent.ACTION_SEND_MULTIPLE -> intent.getParcelableArrayListExtra(Intent.EXTRA_STREAM, Uri::class.java) ?: emptyList()
            else -> emptyList()
        }
        if (shared.isNotEmpty()) stageFiles(shared)
    }

    override fun onNewIntent(intent: Intent) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleShare(intent)
    }

    private fun discover() {
        var found: List<DiscoveredPc> = emptyList()
        runTask(getString(R.string.discover_action), onSuccess = {
            if (found.isNotEmpty()) {
                val lastId = prefs.getString("last_id", null)
                val lastIp = prefs.getString("ip", null)
                val labels = found.map { pc ->
                    val previous = if (pc.id != null && pc.id == lastId || pc.id == null && pc.ip == lastIp)
                        getString(R.string.last_selected) else ""
                    pc.label(getString(R.string.legacy_pc)) + previous
                }
                AlertDialog.Builder(this).setTitle(R.string.select_pc)
                    .setItems(labels.toTypedArray()) { _, which -> selectPc(found[which]) }
                    .setNegativeButton(R.string.enter_ipv4_manually) { _, _ -> enterIp() }
                    .show()
            }
        }) { task, _ ->
            val message = "ROCKET_DISCOVER_V1".toByteArray(StandardCharsets.US_ASCII)
            DatagramSocket().use { socket ->
                task.register(socket)
                socket.broadcast = true
                val broadcasts = mutableSetOf(InetAddress.getByName("255.255.255.255"))
                NetworkInterface.getNetworkInterfaces()?.toList()?.forEach { nic ->
                    if (nic.isUp && !nic.isLoopback) nic.interfaceAddresses.forEach { iface ->
                        if (iface.address is Inet4Address) iface.broadcast?.let(broadcasts::add)
                    }
                }
                var sent = false
                broadcasts.forEach { address ->
                    task.check()
                    try {
                        socket.send(DatagramPacket(message, message.size, address, 42069))
                        sent = true
                    } catch (_: IOException) { /* another interface may still work */ }
                }
                if (!sent) throw RocketError(getString(R.string.broadcast_error))
                val replies = PcDiscovery()
                val deadline = SystemClock.elapsedRealtime() + 2000
                while (true) {
                    task.check()
                    val remaining = deadline - SystemClock.elapsedRealtime()
                    if (remaining <= 0) break
                    socket.soTimeout = minOf(remaining, 500L).toInt().coerceAtLeast(1)
                    val reply = DatagramPacket(ByteArray(1024), 1024)
                    try { socket.receive(reply) } catch (_: SocketTimeoutException) { continue }
                    replies.add(reply.address, reply.port, reply.data, reply.length)
                }
                found = replies.results()
                if (found.isEmpty()) getString(R.string.no_pcs_found)
                else getString(R.string.pcs_found, pcCount(found.size))
            }
        }
    }

    private fun selectPc(pc: DiscoveredPc) {
        var verified: PcInfo? = null
        runTask(getString(R.string.connect_pc, pc.ip), onSuccess = { usePc(pc, checkNotNull(verified)) }) { task, _ ->
            try {
                val info = RocketProtocol(this, pc.ip, task).use { it.ping() }
                if (pc.id != null && pc.id != info.id) throw RocketError(getString(R.string.pc_id_mismatch))
                verified = info
                getString(R.string.pc_verified, info.name, pc.ip)
            } catch (e: Exception) {
                throw RocketError(getString(R.string.pc_verification_error, pc.ip, userFacingError(e)), e)
            }
        }
    }

    override fun onResume() {
        super.onResume()
        resumed = true
        schedulePoll(0)
        if (!offeredSelection && prefs.getString("ip", "").isNullOrBlank()) {
            offeredSelection = true
            AlertDialog.Builder(this).setMessage(R.string.offer_find_pc)
                .setPositiveButton(R.string.find_pc) { _, _ -> discover() }
                .setNegativeButton(R.string.enter_ipv4_manually) { _, _ -> enterIp() }
                .setNeutralButton(android.R.string.cancel, null).show()
        }
    }

    override fun onPause() {
        resumed = false
        main.removeCallbacks(pollTick)
        polling?.cancel()
        polling = null
        super.onPause()
    }

    override fun onStop() {
        if (active != null) {
            active?.cancel() // No background transfer service.
            scene.reset()
        }
        super.onStop()
    }

    override fun onDestroy() {
        polling?.cancel()
        active?.cancel()
        active = null
        super.onDestroy()
    }

    companion object {
        private const val PICK_FILES = 1
        private const val PICK_FOLDER = 2
    }
}
