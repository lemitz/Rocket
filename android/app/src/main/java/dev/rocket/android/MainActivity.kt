package dev.rocket.android

import android.app.Activity
import android.app.AlertDialog
import android.content.Intent
import android.content.pm.ApplicationInfo
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.os.SystemClock
import android.util.Log
import android.view.View
import android.view.ViewGroup
import android.view.WindowInsetsController
import android.view.WindowManager
import android.window.OnBackInvokedDispatcher
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.Toast
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

/**
 * One screen: pick files, slide, fly. Everything else (which PC, where files land, checks) lives
 * in [SettingsPanel]. The phone initiates every TCP connection; while this screen is open it also
 * polls the selected PC for files the PC addressed to this phone and receives them on its own.
 */
class MainActivity : Activity(), LaunchView.Host, SettingsPanel.Host {
    private val main = Handler(Looper.getMainLooper())
    private val prefs by lazy { getSharedPreferences("rocket", MODE_PRIVATE) }
    private lateinit var scene: LaunchView
    private lateinit var settings: SettingsPanel
    private val selected = mutableListOf<Uri>()
    private var uploads: List<UploadFile> = emptyList()
    private var selectionVersion = 0
    private var active: TransferTask? = null
    private var polling: TransferTask? = null
    private var resumed = false
    private var mismatchedPc = false
    private var settingsOpen = false
    private var candidates: List<DiscoveredPc> = emptyList()
    private var discovering = false
    private var discoveryCallbacks = mutableListOf<(List<DiscoveredPc>) -> Unit>()
    private var pcReachable: Boolean? = null
    private var pollFailures = 0
    private var lastRediscovery = 0L
    private val pollTick = Runnable { pollOnce() }
    private val retryDiscovery = Runnable {
        if (resumed && !pcKnown() && !discovering) discoverPcs(true)
    }
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
        val frame = FrameLayout(this)
        scene = LaunchView(this).also { it.host = this }
        frame.addView(scene, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        settings = SettingsPanel(this, this)
        settings.view.visibility = View.GONE
        frame.addView(settings.view, ViewGroup.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT))
        setContentView(frame)
        window.insetsController?.setSystemBarsAppearance(0,
            WindowInsetsController.APPEARANCE_LIGHT_STATUS_BARS or WindowInsetsController.APPEARANCE_LIGHT_NAVIGATION_BARS)
        onBackInvokedDispatcher.registerOnBackInvokedCallback(OnBackInvokedDispatcher.PRIORITY_DEFAULT) { goBack() }
        showSelection()
        handleShare(intent)
    }

    private fun goBack() {
        when {
            settingsOpen -> closeSettings()
            scene.phase == LaunchView.Phase.IDLE && sheetShown -> { sheetShown = false; scene.closeSheet() }
            active != null -> onCancel()
            scene.phase == LaunchView.Phase.OK || scene.phase == LaunchView.Phase.FAIL -> scene.reset()
            else -> finish()
        }
    }

    // ================================================================ selection

    private fun pcIp() = prefs.getString("ip", "").orEmpty()
    private fun pcKnown() = pcIp().isNotBlank() && !mismatchedPc

    private fun showSelection() {
        scene.setSelection(uploads.size, uploads.sumOf { it.size }, uploads.singleOrNull()?.name)
        updateTarget()
    }

    private fun updateTarget() {
        val name = prefs.getString("last_name", null)?.takeIf { prefs.getString("last_ip", null) == pcIp() }
            ?: getString(R.string.pc_fallback)
        val known = pcKnown()
        val label: String; val dot: Int; val pickable: Boolean
        when {
            known && pcReachable == false -> { label = getString(R.string.pc_offline, name); dot = Pal.warn; pickable = true }
            known -> { label = getString(R.string.to_pc, name); dot = Pal.good; pickable = true }
            mismatchedPc -> { label = getString(R.string.pc_reselect); dot = Pal.warn; pickable = true }
            discovering -> { label = getString(R.string.pc_searching); dot = Pal.warn; pickable = false }
            candidates.size > 1 -> { label = getString(R.string.pc_pick); dot = Pal.warn; pickable = true }
            else -> { label = getString(R.string.pc_none); dot = Pal.warn; pickable = true }
        }
        scene.setTarget(label, dot, pickable, known && uploads.isNotEmpty() && active == null)
    }

    private fun stageFiles(uris: List<Uri>) {
        if (active != null) return
        selectionVersion++
        val version = selectionVersion
        selected.clear()
        selected.addAll(uris.distinct())
        uploads = emptyList()
        showSelection()
        if (selected.isEmpty()) return
        val snapshot = selected.toList()
        Thread {
            var files: List<UploadFile>? = null
            var failure: Exception? = null
            try { files = snapshot.map { uploadFile(this, it) } }
            catch (e: Exception) { failure = e }
            main.post {
                if (selectionVersion != version || isFinishing || isDestroyed) return@post
                if (failure != null) {
                    selected.clear()
                    Toast.makeText(this, getString(R.string.selection_error, userFacingError(failure)), Toast.LENGTH_LONG).show()
                } else uploads = checkNotNull(files)
                showSelection()
            }
        }.start()
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

    // ================================================================ LaunchView.Host

    private var sheetShown = false

    override fun onChoose() {
        if (active != null) return
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
            addCategory(Intent.CATEGORY_OPENABLE)
            type = "*/*"
            putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
        }, PICK_FILES)
    }

    override fun onClear() {
        selectionVersion++
        selected.clear(); uploads = emptyList()
        showSelection()
    }

    override fun onSettings() {
        settings.refresh()
        settings.clearCheck()
        settings.view.visibility = View.VISIBLE
        settings.view.alpha = 0f
        settings.view.animate().alpha(1f).setDuration(180).start()
        settingsOpen = true
    }

    private fun closeSettings() {
        if (!settingsOpen) return
        settingsOpen = false
        settings.view.animate().alpha(0f).setDuration(150).withEndAction {
            if (!settingsOpen) settings.view.visibility = View.GONE
        }.start()
    }

    override fun onLaunch() = sendSelected()

    override fun onCancel() {
        val task = active ?: return
        task.cancel()
    }

    override fun onDismiss() = scene.reset()

    override fun onTarget() {
        if (discovering) return
        val known = pcKnown()
        val savedId = prefs.getString("last_id", null)
        discoverPcs(false) { found ->
            when {
                found.isEmpty() -> {
                    if (known) Toast.makeText(this, R.string.no_pcs_found, Toast.LENGTH_SHORT).show()
                    else onSettings()
                }
                found.size == 1 && (known && found[0].id == savedId) -> {}
                found.size == 1 && !known -> {} // auto-selected after discovery
                else -> showPcSheet()
            }
        }
    }

    private fun showPcSheet() {
        val current = prefs.getString("last_id", null)
        val items = candidates.map { "${it.name ?: getString(R.string.legacy_pc)} · ${it.ip}" }
        sheetShown = items.isNotEmpty()
        scene.openSheet(items, candidates.indexOfFirst { it.id != null && it.id == current })
    }

    override fun onSheetPick(index: Int) {
        sheetShown = false
        scene.closeSheet()
        candidates.getOrNull(index)?.let { selectPc(it, false) }
    }

    override fun onSheetClose() { sheetShown = false; scene.closeSheet() }

    // ================================================================ SettingsPanel.Host

    override fun pcSummary(): String {
        val ip = pcIp()
        if (ip.isBlank()) return getString(R.string.pc_not_selected)
        val name = prefs.getString("last_name", null)?.takeIf { prefs.getString("last_ip", null) == ip } ?: getString(R.string.pc_fallback)
        return "$name · $ip"
    }

    override fun folderSummary(): String {
        val tree = prefs.getString("tree", null) ?: return getString(R.string.destination_downloads)
        val last = Uri.parse(tree).lastPathSegment?.substringAfterLast(':')?.ifBlank { null }
        return getString(R.string.destination_folder, last ?: tree)
    }

    override fun deviceSummary(): String {
        val version = try { packageManager.getPackageInfo(packageName, 0).versionName } catch (_: Exception) { "" }
        return "$deviceName · v$version"
    }

    override fun onBack() = closeSettings()

    override fun onFindPc() {
        discoverPcs(false) { found ->
            if (found.isEmpty()) { Toast.makeText(this, R.string.no_pcs_found, Toast.LENGTH_LONG).show(); return@discoverPcs }
            val lastId = prefs.getString("last_id", null)
            val labels = found.map { pc ->
                pc.label(getString(R.string.legacy_pc)) + if (pc.id != null && pc.id == lastId) getString(R.string.last_selected) else ""
            }
            AlertDialog.Builder(this).setTitle(R.string.select_pc)
                .setItems(labels.toTypedArray()) { _, which -> selectPc(found[which], false) }
                .setNegativeButton(R.string.enter_ipv4_manually) { _, _ -> onEnterIp() }
                .show()
        }
    }

    override fun onEnterIp() {
        val edit = EditText(this).apply {
            hint = getString(R.string.ip_hint)
            setSingleLine(true)
            inputType = android.text.InputType.TYPE_CLASS_PHONE
            setText(prefs.getString("unverified_ip", null) ?: prefs.getString("ip", ""))
        }
        val pad = (resources.displayMetrics.density * 20).toInt()
        val holder = LinearLayout(this).apply { setPadding(pad, 0, pad, 0); addView(edit) }
        AlertDialog.Builder(this).setTitle(R.string.enter_ipv4_manually).setView(holder)
            .setPositiveButton(R.string.select_pc) { _, _ ->
                val address = edit.text.toString().trim()
                try {
                    pcAddress(this, address)
                    selectPc(DiscoveredPc(address, null, null), false)
                } catch (e: Exception) { Toast.makeText(this, userFacingError(e), Toast.LENGTH_LONG).show() }
            }.setNegativeButton(android.R.string.cancel, null).show()
    }

    override fun onChooseFolder() {
        startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT_TREE).apply {
            addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
                Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION)
        }, PICK_FOLDER)
    }

    override fun onUseDownloads() {
        clearFolder()
        settings.refresh()
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

    override fun onCheck() {
        settings.showCheck(listOf(true to getString(R.string.checking)))
        val ip = pcIp()
        val expectedId = prefs.getString("last_id", null)
        Thread {
            val lines = mutableListOf<Pair<Boolean, String>>()
            val task = TransferTask(getString(R.string.error_cancelled))
            if (ip.isBlank()) lines += false to getString(R.string.pc_not_selected)
            else try {
                val started = SystemClock.elapsedRealtime()
                val info = RocketProtocol(this, ip, task).use { it.ping() }
                val ms = SystemClock.elapsedRealtime() - started
                if (expectedId != null && info.id != null && info.id != expectedId)
                    lines += false to getString(R.string.pc_id_mismatch)
                else lines += true to getString(R.string.check_pc_ok, info.name, ms)
            } catch (e: Exception) { lines += false to getString(R.string.check_pc_fail, userFacingError(e)) }
            try {
                val found = broadcastDiscovery(task)
                lines += found.isNotEmpty() to (if (found.isEmpty()) getString(R.string.no_pcs_found)
                    else getString(R.string.pcs_found, pcCount(found.size)))
            } catch (e: Exception) { lines += false to getString(R.string.broadcast_error) }
            main.post { if (!isDestroyed) settings.showCheck(lines) }
        }.start()
    }

    // ================================================================ PC selection

    private fun usePc(pc: DiscoveredPc, info: PcInfo) {
        // Explicit selection (or the only PC found); discovery alone never replaces a chosen PC.
        prefs.edit().putString("ip", pc.ip).putString("last_ip", pc.ip)
            .putString("last_id", info.id).putString("last_name", info.name)
            .remove("unverified_ip").apply()
        polling?.cancel()
        polling = null
        mismatchedPc = false
        pcReachable = true
        pollFailures = 0
        updateTarget()
        settings.refresh()
        schedulePoll(0)
    }

    private fun selectPc(pc: DiscoveredPc, silent: Boolean) {
        val task = TransferTask(getString(R.string.error_cancelled))
        Thread {
            var info: PcInfo? = null
            var failure: Exception? = null
            try {
                info = RocketProtocol(this, pc.ip, task).use { it.ping() }
                if (pc.id != null && pc.id != info.id) throw RocketError(getString(R.string.pc_id_mismatch))
            } catch (e: Exception) { failure = e }
            main.post {
                if (isDestroyed) return@post
                if (failure != null) {
                    if (!silent) Toast.makeText(this, getString(R.string.pc_verification_error, pc.ip, userFacingError(failure)),
                        Toast.LENGTH_LONG).show()
                } else usePc(pc, checkNotNull(info))
            }
        }.start()
    }

    /** After a search: follow the chosen PC to a new address, or take the only PC when none is chosen yet. */
    private fun afterDiscovery(found: List<DiscoveredPc>) {
        val savedId = prefs.getString("last_id", null)
        val ip = pcIp()
        if (savedId != null) {
            val same = found.firstOrNull { it.id == savedId }
            if (same != null && (same.ip != ip || mismatchedPc)) {
                prefs.edit().putString("ip", same.ip).putString("last_ip", same.ip).apply()
                mismatchedPc = false; pcReachable = null; pollFailures = 0
                settings.refresh()
                schedulePoll(0)
            }
        } else if (ip.isBlank() && found.size == 1) {
            selectPc(found[0], true)
        }
    }

    private fun discoverPcs(silent: Boolean, done: (List<DiscoveredPc>) -> Unit = {}) {
        discoveryCallbacks.add(done)
        if (discovering) return
        discovering = true
        updateTarget()
        val task = TransferTask(getString(R.string.error_cancelled))
        Thread {
            var found: List<DiscoveredPc> = emptyList()
            var failure: Exception? = null
            try { found = broadcastDiscovery(task) } catch (e: Exception) { failure = e }
            main.post {
                discovering = false
                val callbacks = discoveryCallbacks
                discoveryCallbacks = mutableListOf()
                if (isDestroyed) return@post
                if (failure == null) { candidates = found; afterDiscovery(found) }
                else if (!silent) Toast.makeText(this, R.string.broadcast_error, Toast.LENGTH_LONG).show()
                updateTarget()
                callbacks.forEach { it(found) }
                if (resumed && !pcKnown() && failure == null) main.postDelayed(retryDiscovery, 5000)
            }
        }.start()
    }

    private fun broadcastDiscovery(task: TransferTask): List<DiscoveredPc> {
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
            val deadline = SystemClock.elapsedRealtime() + 1800
            while (true) {
                task.check()
                val remaining = deadline - SystemClock.elapsedRealtime()
                if (remaining <= 0) break
                socket.soTimeout = minOf(remaining, 500L).toInt().coerceAtLeast(1)
                val reply = DatagramPacket(ByteArray(1024), 1024)
                try { socket.receive(reply) } catch (_: SocketTimeoutException) { continue }
                replies.add(reply.address, reply.port, reply.data, reply.length)
            }
            return replies.results()
        }
    }

    // ================================================================ transfers

    private fun runTransfer(incoming: Boolean, count: Int, existingTask: TransferTask? = null,
                            onSuccess: (() -> Unit)? = null,
                            operation: (TransferTask, TransferProgress) -> Unit) {
        if (active != null) return
        if (existingTask == null) { polling?.cancel(); polling = null }
        main.removeCallbacks(pollTick)
        val task = existingTask ?: TransferTask(getString(R.string.error_cancelled))
        active = task
        closeSettings()
        sheetShown = false
        scene.closeSheet()
        scene.launch(incoming)
        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
        updateTarget()
        var latest: TransferSnapshot? = null
        val progress = TransferProgress(count) { snapshot -> main.post {
            if (active === task && !task.isCancelled()) latest = snapshot
        } }
        val ticker = object : Runnable {
            override fun run() {
                if (active !== task || task.isCancelled()) return
                latest?.let { s -> scene.setProgress(if (s.total != null && s.total > 0) s.bytes.toFloat() / s.total else 0f, s.bytes > 0) }
                main.postDelayed(this, 100)
            }
        }
        main.postDelayed(ticker, 100)
        Thread {
            var failure: Exception? = null
            try { operation(task, progress); task.check() }
            catch (e: Exception) {
                failure = e
                if (applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0 && !task.isCancelled())
                    Log.w("RocketTransfer", "Operation failed (raw diagnostics)", e)
            }
            val snapshot = if (failure == null && !task.isCancelled()) progress.finish() else null
            main.post {
                if (active !== task) return@post
                active = null
                window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                when {
                    task.isCancelled() -> { pollFailures = 0; scene.reset() }
                    failure != null -> {
                        scene.fail(userFacingError(checkNotNull(failure)))
                    }
                    snapshot != null -> { scene.succeed(snapshot); onSuccess?.invoke() }
                }
                showSelection()
                schedulePoll()
            }
        }.start()
    }

    private fun sendSelected() {
        if (active != null) return
        val files = uploads.toList()
        val address = pcIp()
        if (files.isEmpty() || address.isBlank() || mismatchedPc) return
        runTransfer(false, files.size, onSuccess = {
            selectionVersion++
            selected.clear()
            uploads = emptyList()
        }) { task, progress ->
            progress.setTotal(files.map { it.size })
            RocketProtocol(this, address, task).use { client ->
                val expectedId = prefs.getString("last_id", null)
                if (expectedId != null && client.ping().id != expectedId)
                    throw RocketError(getString(R.string.pc_id_mismatch))
                for (file in files) {
                    task.check()
                    client.put(contentResolver, file, progress::transferred)
                    progress.confirmed()
                }
            }
        }
    }

    private fun pollOnce() {
        if (!resumed || active != null || polling != null) return
        val address = pcIp()
        if (address.isBlank()) return
        val task = TransferTask(getString(R.string.error_cancelled))
        polling = task
        Thread {
            var client: RocketProtocol? = null
            var offers: List<Offer> = emptyList()
            var identityMismatch = false
            var unreachable = false
            try {
                client = RocketProtocol(this, address, task)
                val expectedId = prefs.getString("last_id", null)
                if (expectedId != null && client.ping().id != expectedId)
                    throw RocketError(getString(R.string.pc_id_mismatch))
                offers = client.poll(deviceId, deviceName)
            } catch (e: Exception) {
                // Network failures are expected during foreground polling; never prompt for them.
                identityMismatch = e is RocketError && e.message == getString(R.string.pc_id_mismatch)
                unreachable = !identityMismatch
            }
            main.post {
                if (polling === task && resumed && !task.isCancelled()) {
                    if (identityMismatch) {
                        mismatchedPc = true
                        updateTarget()
                        rediscover()
                    } else if (unreachable) {
                        pollFailures++
                        if (pollFailures >= 2 && pcReachable != false) { pcReachable = false; updateTarget() }
                        if (pollFailures >= 3) rediscover()
                    } else {
                        pollFailures = 0
                        if (pcReachable != true) { pcReachable = true; updateTarget() }
                    }
                }
                if (polling !== task || !resumed || active != null || pcIp() != address || task.isCancelled()) {
                    client?.close()
                    if (polling === task) { polling = null; schedulePoll() }
                } else if (offers.isEmpty()) {
                    client?.close()
                    polling = null
                    schedulePoll()
                } else {
                    polling = null
                    receiveOffers(task, checkNotNull(client), offers)
                }
            }
        }.start()
    }

    /** The chosen PC may have a new address (DHCP): look for its public ID, at most every 15 s. */
    private fun rediscover() {
        val nowMs = SystemClock.elapsedRealtime()
        if (discovering || prefs.getString("last_id", null) == null || nowMs - lastRediscovery < 15_000) return
        lastRediscovery = nowMs
        discoverPcs(true)
    }

    private fun receiveOffers(task: TransferTask, client: RocketProtocol, offers: List<Offer>) {
        val tree = prefs.getString("tree", null)?.let(Uri::parse)
        runTransfer(true, offers.size, existingTask = task) { _, progress ->
            client.use { connection ->
                val sizes = offers.map { it.size }.toMutableList()
                progress.setTotal(sizes)
                val store = DownloadStore(this, tree)
                for ((index, offer) in offers.withIndex()) {
                    task.check()
                    connection.offerGet(deviceId, offer, store,
                        { size -> sizes[index] = size; progress.setTotal(sizes) }, progress::transferred)
                    task.check()
                    // Only acknowledge after CRC32C, stream close and publication succeeded.
                    connection.offerDone(deviceId, offer.id)
                    progress.confirmed()
                }
            }
        }
    }

    private fun schedulePoll(delay: Long = 1000) {
        main.removeCallbacks(pollTick)
        if (resumed && active == null && polling == null && !mismatchedPc && pcIp().isNotBlank())
            main.postDelayed(pollTick, delay)
    }

    // ================================================================ system callbacks

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
                settings.refresh()
            } catch (_: SecurityException) {
                Toast.makeText(this, R.string.folder_access_error, Toast.LENGTH_LONG).show()
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

    override fun onResume() {
        super.onResume()
        resumed = true
        schedulePoll(0)
        if (!pcKnown() && !discovering) discoverPcs(true)
    }

    override fun onPause() {
        resumed = false
        main.removeCallbacks(pollTick)
        main.removeCallbacks(retryDiscovery)
        polling?.cancel()
        polling = null
        super.onPause()
    }

    override fun onStop() {
        if (active != null) {
            active?.cancel() // No background transfer service.
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
