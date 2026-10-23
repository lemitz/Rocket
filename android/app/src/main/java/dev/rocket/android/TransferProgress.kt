package dev.rocket.android

import android.content.Context
import android.os.SystemClock

internal data class TransferSnapshot(
    val bytes: Long,
    val total: Long?,
    val completed: Int,
    val fileCount: Int,
    val elapsedNanos: Long
) {
    val bytesPerSecond: Double get() = if (elapsedNanos > 0) bytes.toDouble() * 1_000_000_000.0 / elapsedNanos else 0.0
}

// Used only by the transfer thread. The UI receives immutable, rate-limited snapshots.
internal class TransferProgress(private val fileCount: Int, private val report: (TransferSnapshot) -> Unit) {
    private val started = SystemClock.elapsedRealtimeNanos()
    private var lastReport = started
    private var bytes = 0L
    private var total: Long? = null
    private var completed = 0

    fun setTotal(sizes: List<Long>) {
        total = sizes.takeIf { list -> list.all { it >= 0 } }?.fold(0L) { sum, size ->
            if (Long.MAX_VALUE - sum < size) Long.MAX_VALUE else sum + size
        }
        emit(true)
    }

    fun transferred(count: Int) {
        bytes = if (bytes > Long.MAX_VALUE - count) Long.MAX_VALUE else bytes + count
        emit(false)
    }

    fun confirmed() {
        completed++
        emit(false)
    }

    fun finish(): TransferSnapshot = snapshot(SystemClock.elapsedRealtimeNanos())

    private fun snapshot(now: Long) = TransferSnapshot(bytes, total, completed, fileCount, (now - started).coerceAtLeast(0))

    private fun emit(force: Boolean) {
        val now = SystemClock.elapsedRealtimeNanos()
        if (force || now - lastReport >= 100_000_000L) {
            lastReport = now
            report(snapshot(now))
        }
    }
}

internal fun Context.fileCount(count: Int): String =
    resources.getQuantityString(R.plurals.file_count, count, count)

internal fun Context.pcCount(count: Int): String =
    resources.getQuantityString(R.plurals.pc_count, count, count)

internal fun Context.readableBytes(bytes: Long): String {
    if (bytes < 1024) return getString(R.string.bytes_value, bytes)
    var value = bytes.toDouble()
    var unit = 0
    val units = intArrayOf(R.string.bytes_value, R.string.unit_kib, R.string.unit_mib,
        R.string.unit_gib, R.string.unit_tib)
    while (value >= 1024 && unit < units.lastIndex) { value /= 1024; unit++ }
    return getString(R.string.scaled_bytes_value, value, getString(units[unit]))
}

internal fun Context.readableTime(nanos: Long): String =
    getString(R.string.seconds_value, nanos / 1_000_000_000.0)

internal fun Context.readableSpeed(bytesPerSecond: Long): String =
    getString(R.string.speed_value, readableBytes(bytesPerSecond.coerceAtLeast(0)))
