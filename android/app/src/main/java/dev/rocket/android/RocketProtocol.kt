package dev.rocket.android

import android.content.ContentResolver
import android.content.Context
import android.content.pm.ApplicationInfo
import android.net.Uri
import android.provider.OpenableColumns
import android.util.Log
import org.json.JSONObject
import java.io.BufferedInputStream
import java.io.BufferedOutputStream
import java.io.DataInputStream
import java.io.DataOutputStream
import java.io.InputStream
import java.net.InetAddress
import java.net.InetSocketAddress
import java.net.Socket
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.util.zip.CRC32C

internal data class Offer(val id: String, val name: String, val size: Long, val mtime: Long)
internal data class PcInfo(val name: String, val id: String?)
internal data class UploadFile(val uri: Uri, val name: String, val size: Long)

internal fun uploadFile(context: Context, uri: Uri): UploadFile {
    val resolver = context.contentResolver
    val (name, size) = resolver.query(uri, arrayOf(OpenableColumns.DISPLAY_NAME, OpenableColumns.SIZE),
        null, null, null)?.use { cursor ->
        if (!cursor.moveToFirst()) throw RocketError(context.getString(R.string.error_file_metadata))
        val nameIndex = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        val sizeIndex = cursor.getColumnIndex(OpenableColumns.SIZE)
        Pair(if (nameIndex >= 0 && !cursor.isNull(nameIndex)) cursor.getString(nameIndex) else null,
            if (sizeIndex >= 0 && !cursor.isNull(sizeIndex)) cursor.getLong(sizeIndex) else null)
    } ?: throw RocketError(context.getString(R.string.error_file_metadata))
    if (name == null || name.isEmpty() || name.toByteArray(StandardCharsets.UTF_8).size > 200) {
        throw RocketError(context.getString(R.string.error_file_name))
    }
    if (size == null || size < 0 || size > (1L shl 40)) {
        throw RocketError(context.getString(R.string.error_file_size))
    }
    return UploadFile(uri, name, size)
}

internal fun pcAddress(context: Context, text: String): InetAddress {
    val parts = text.trim().split('.')
    if (parts.size != 4 || parts.any { it.isEmpty() || it.length > 3 || it.any { c -> c !in '0'..'9' } }) {
        throw RocketError(context.getString(R.string.error_ip_required))
    }
    val bytes = parts.map { it.toIntOrNull()?.takeIf { n -> n in 0..255 }
        ?: throw RocketError(context.getString(R.string.error_invalid_ip)) }.map { it.toByte() }.toByteArray()
    return InetAddress.getByAddress(bytes)
}

// One TCP connection per operation. A failed transfer is never reused.
internal class RocketProtocol(private val context: Context, ip: String, private val task: TransferTask) : AutoCloseable {
    private val socket = Socket()
    private val input: DataInputStream
    private val output: DataOutputStream

    init {
        task.register(socket)
        try {
            socket.connect(InetSocketAddress(pcAddress(context, ip), 42070), 3000)
            socket.soTimeout = 120_000
            input = DataInputStream(BufferedInputStream(socket.getInputStream()))
            output = DataOutputStream(BufferedOutputStream(socket.getOutputStream()))
        } catch (e: Exception) {
            socket.close()
            throw e
        }
    }

    private fun frame(json: JSONObject) {
        task.check()
        val bytes = json.toString().toByteArray(StandardCharsets.UTF_8)
        if (bytes.isEmpty() || bytes.size > 65536) throw RocketError(context.getString(R.string.error_header_size))
        output.writeInt(bytes.size) // network byte order (big endian)
        output.write(bytes)
        output.flush()
    }

    private fun readFrame(): JSONObject {
        task.check()
        val length = input.readInt()
        if (length !in 1..65536) throw RocketError(context.getString(R.string.error_frame_length, length))
        val bytes = ByteArray(length)
        input.readFully(bytes)
        if (bytes[0] != '{'.code.toByte()) throw RocketError(context.getString(R.string.error_invalid_json))
        return try {
            val text = StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(bytes)).toString()
            JSONObject(text)
        } catch (e: Exception) { throw RocketError(context.getString(R.string.error_invalid_json), e) }
    }

    private fun rejected(response: JSONObject, fallback: Int): Nothing {
        // Remote daemon text is not localized; retain it only for debugging.
        if (context.applicationInfo.flags and ApplicationInfo.FLAG_DEBUGGABLE != 0)
            Log.w("RocketTransfer", "PC rejection: ${response.optString("error")}")
        throw RocketError(context.getString(fallback))
    }

    private fun request(cmd: String, fields: JSONObject = JSONObject()): JSONObject {
        fields.put("v", 1).put("cmd", cmd)
        frame(fields)
        val response = readFrame()
        if (!response.getBoolean("ok")) rejected(response, R.string.error_pc_rejected)
        return response
    }

    fun ping(): PcInfo {
        socket.soTimeout = 3000
        val response = request("ping")
        val name = response.getString("name")
        if (name.isBlank() || name.any { it.isISOControl() }) throw RocketError(context.getString(R.string.error_pc_name))
        val id = if (response.has("id")) response.getString("id") else null
        if (id != null && !validNodeId(id)) throw RocketError(context.getString(R.string.error_pc_id))
        return PcInfo(name, id)
    }

    fun poll(device: String, deviceName: String): List<Offer> {
        socket.soTimeout = 3000
        val response = request("poll", JSONObject().put("device", device).put("device_name", deviceName))
        val offers = response.getJSONArray("offers")
        if (offers.length() > 1000) throw RocketError(context.getString(R.string.error_offers))
        return (0 until offers.length()).map { index ->
            val item = offers.getJSONObject(index)
            val id = item.getString("id")
            val name = item.getString("name")
            val size = item.getLong("size")
            val mtime = item.getLong("mtime")
            if (id.isBlank() || id.length > 128 || size !in 0..MAX_FILE_SIZE ||
                name.isEmpty() || name.toByteArray(StandardCharsets.UTF_8).size > 200 || name == "." || name == ".." ||
                name.any { it == '/' || it == '\\' || it.code < 32 || it.code == 127 }) {
                throw RocketError(context.getString(R.string.error_offers))
            }
            Offer(id, name, size, mtime)
        }.distinctBy { it.id }
    }

    fun offerGet(device: String, offer: Offer, store: DownloadStore,
                 onSize: (Long) -> Unit = {}, onBytes: (Int) -> Unit = {}): String {
        socket.soTimeout = 120_000
        val response = request("offer-get", JSONObject().put("device", device).put("id", offer.id))
        return receive(offer.name, response, store, onSize, onBytes)
    }

    fun offerDone(device: String, id: String) {
        socket.soTimeout = 120_000
        request("offer-done", JSONObject().put("device", device).put("id", id))
    }

    fun put(resolver: ContentResolver, file: UploadFile, onBytes: (Int) -> Unit = {}): String {
        // Open the source BEFORE the PUT acknowledgement to avoid leaving a waiting server.
        val source = resolver.openInputStream(file.uri) ?: throw RocketError(context.getString(R.string.error_open_file))
        source.use {
            socket.soTimeout = 120_000
            request("put", JSONObject().put("name", file.name).put("size", file.size))
            socket.soTimeout = 3_600_000
            val hash = CRC32C()
            copyExactly(source, output, file.size, hash, onBytes)
            // A provider reporting a size smaller than its stream must not silently truncate it.
            if (source.read() != -1) throw RocketError(context.getString(R.string.error_file_changed))
            output.flush()
            frame(JSONObject().put("hash", "%08x".format(hash.value)))
            socket.soTimeout = 120_000
            val result = readFrame()
            if (!result.getBoolean("ok")) rejected(result, R.string.error_upload_failed)
            return result.optString("saved", file.name)
        }
    }

    private fun receive(name: String, response: JSONObject, store: DownloadStore,
                        onSize: (Long) -> Unit, onBytes: (Int) -> Unit): String {
        val size = response.getLong("size")
        if (size < 0 || size > MAX_FILE_SIZE) throw RocketError(context.getString(R.string.error_download_size))
        onSize(size)
        val target = store.create(name)
        try {
            socket.soTimeout = 3_600_000
            val hash = CRC32C()
            copyExactly(input, target.stream, size, hash, onBytes)
            val trailer = readFrame().getString("hash")
            if (trailer != "%08x".format(hash.value)) throw RocketError(context.getString(R.string.error_checksum))
            task.check()
            return target.publish()
        } catch (e: Exception) {
            try { target.abort() } catch (cleanup: Exception) { e.addSuppressed(cleanup) }
            throw e
        }
    }

    private fun copyExactly(source: InputStream, destination: java.io.OutputStream, size: Long, hash: CRC32C,
                            onBytes: (Int) -> Unit) {
        val buffer = ByteArray(256 * 1024)
        var remaining = size
        while (remaining > 0) {
            task.check()
            val count = source.read(buffer, 0, minOf(buffer.size.toLong(), remaining).toInt())
            if (count < 0) throw RocketError(context.getString(R.string.error_early_eof))
            if (count == 0) continue
            destination.write(buffer, 0, count)
            hash.update(buffer, 0, count)
            remaining -= count
            onBytes(count) // Count only after a successful write (GET: written to destination).
        }
    }

    override fun close() = socket.close()

    companion object { private const val MAX_FILE_SIZE = 1L shl 40 }
}
