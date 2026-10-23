package dev.rocket.android

import android.content.ContentResolver
import android.content.ContentValues
import android.content.Context
import android.net.Uri
import android.os.Environment
import android.provider.DocumentsContract
import android.provider.MediaStore
import android.webkit.MimeTypeMap
import java.io.OutputStream
import java.util.UUID

internal interface DownloadTarget {
    val stream: OutputStream
    // Only called after all bytes and the trailer checksum have been verified.
    fun publish(): String
    fun abort()
}

internal class DownloadStore(private val context: Context, private val tree: Uri?) {
    private val resolver: ContentResolver = context.contentResolver

    fun create(name: String): DownloadTarget {
        if (name.isEmpty() || name.length > 200 || name == "." || name == ".." ||
            name.any { it == '/' || it == '\\' || it.code < 32 || it.code == 127 }) {
            throw RocketError(context.getString(R.string.error_unsafe_destination))
        }
        return if (tree == null) createMedia(name) else createDocument(tree, name)
    }

    private fun mime(name: String): String {
        val extension = name.substringAfterLast('.', "").lowercase()
        return MimeTypeMap.getSingleton().getMimeTypeFromExtension(extension) ?: "application/octet-stream"
    }

    private fun unique(name: String, exists: (String) -> Boolean): String {
        val dot = name.lastIndexOf('.')
        val stem = if (dot > 0) name.substring(0, dot) else name
        val ext = if (dot > 0) name.substring(dot) else ""
        for (index in 0..9999) {
            val candidate = if (index == 0) name else "$stem ($index)$ext"
            if (!exists(candidate)) return candidate
        }
        throw RocketError(context.getString(R.string.error_too_many_names))
    }

    private fun createMedia(name: String): DownloadTarget {
        val collection = MediaStore.Downloads.getContentUri(MediaStore.VOLUME_EXTERNAL_PRIMARY)
        val folder = Environment.DIRECTORY_DOWNLOADS + "/"
        fun exists(candidate: String): Boolean = resolver.query(
            collection, arrayOf(MediaStore.MediaColumns._ID),
            "${MediaStore.MediaColumns.DISPLAY_NAME}=? AND ${MediaStore.MediaColumns.RELATIVE_PATH}=?",
            arrayOf(candidate, folder), null
        )?.use { it.moveToFirst() } ?: throw RocketError(context.getString(R.string.error_check_downloads))

        val chosen = unique(name, ::exists)
        val values = ContentValues().apply {
            put(MediaStore.MediaColumns.DISPLAY_NAME, chosen)
            put(MediaStore.MediaColumns.MIME_TYPE, mime(chosen))
            put(MediaStore.MediaColumns.RELATIVE_PATH, folder)
            put(MediaStore.MediaColumns.IS_PENDING, 1)
        }
        val uri = resolver.insert(collection, values) ?: throw RocketError(context.getString(R.string.error_create_download))
        val stream = try { resolver.openOutputStream(uri, "w") ?: throw RocketError(context.getString(R.string.error_open_download)) }
        catch (e: Exception) { resolver.delete(uri, null, null); throw e }
        return object : DownloadTarget {
            override val stream: OutputStream = stream
            private var closed = false
            private fun closeStream() { if (!closed) { closed = true; stream.close() } }
            override fun publish(): String {
                closeStream()
                val update = ContentValues().apply { put(MediaStore.MediaColumns.IS_PENDING, 0) }
                if (resolver.update(uri, update, null, null) != 1) throw RocketError(context.getString(R.string.error_publish_download))
                return resolver.query(uri, arrayOf(MediaStore.MediaColumns.DISPLAY_NAME), null, null, null)
                    ?.use { if (it.moveToFirst()) it.getString(0) else null } ?: chosen
            }
            override fun abort() {
                try { closeStream() } catch (_: Exception) { /* still delete */ }
                resolver.delete(uri, null, null)
            }
        }
    }

    private fun createDocument(tree: Uri, name: String): DownloadTarget {
        val parent = DocumentsContract.buildDocumentUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree))
        fun names(): Set<String> {
            val children = DocumentsContract.buildChildDocumentsUriUsingTree(tree, DocumentsContract.getTreeDocumentId(tree))
            return resolver.query(children, arrayOf(DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null)
                ?.use { cursor -> buildSet { while (cursor.moveToNext()) add(cursor.getString(0)) } }
                ?: throw RocketError(context.getString(R.string.error_list_folder))
        }
        val existing = names()
        val original = unique(name) { it in existing }
        // The part stays in the selected tree; failed/incomplete transfers delete it.
        val partName = ".rocket-${UUID.randomUUID()}.part"
        val part = DocumentsContract.createDocument(resolver, parent, "application/octet-stream", partName)
            ?: throw RocketError(context.getString(R.string.error_create_temp))
        val stream = try { resolver.openOutputStream(part, "w") ?: throw RocketError(context.getString(R.string.error_open_temp)) }
        catch (e: Exception) { DocumentsContract.deleteDocument(resolver, part); throw e }
        return object : DownloadTarget {
            override val stream: OutputStream = stream
            private var closed = false
            private var current = part
            private fun closeStream() { if (!closed) { closed = true; stream.close() } }
            override fun publish(): String {
                closeStream()
                val latest = names()
                val candidate = unique(original) { it in latest }
                // SAF rename is provider-dependent: NOT an atomic no-replace publication.
                current = DocumentsContract.renameDocument(resolver, current, candidate)
                    ?: throw RocketError(context.getString(R.string.error_rename_temp))
                return resolver.query(current, arrayOf(DocumentsContract.Document.COLUMN_DISPLAY_NAME), null, null, null)
                    ?.use { if (it.moveToFirst()) it.getString(0) else null } ?: candidate
            }
            override fun abort() {
                try { closeStream() } catch (_: Exception) { /* still delete */ }
                if (!DocumentsContract.deleteDocument(resolver, current)) throw RocketError(context.getString(R.string.error_remove_temp))
            }
        }
    }
}
