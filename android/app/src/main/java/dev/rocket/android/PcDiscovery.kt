package dev.rocket.android

import java.net.Inet4Address
import java.net.InetAddress
import java.nio.ByteBuffer
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets

internal fun validNodeId(id: String): Boolean =
    id.length == 32 && id.all { it in '0'..'9' || it in 'a'..'f' }

internal data class DiscoveredPc(val ip: String, val id: String?, val name: String?) {
    fun label(legacyName: String): String = "${name ?: legacyName} • $ip" + (id?.let { " • ${it.take(8)}" } ?: "")
}

// Discovery is only a convenience on a trusted LAN, not an identity or authentication mechanism.
internal class PcDiscovery {
    private val pcs = LinkedHashMap<String, DiscoveredPc>()

    fun add(source: InetAddress, port: Int, bytes: ByteArray, length: Int) {
        if (port != 42069 || source !is Inet4Address || !isLocalAddress(source) || length !in 1..512) return
        val text = try {
            StandardCharsets.UTF_8.newDecoder()
                .onMalformedInput(CodingErrorAction.REPORT)
                .onUnmappableCharacter(CodingErrorAction.REPORT)
                .decode(ByteBuffer.wrap(bytes, 0, length)).toString()
        } catch (_: Exception) { return }
        val id: String?
        val name: String?
        if (text == "ROCKET_HERE_V1") {
            id = null
            name = null
        } else {
            val fields = text.split('|', limit = 3)
            if (fields.size != 3 || fields[0] != "ROCKET_HERE_V1" || !validNodeId(fields[1]) ||
                fields[2].isBlank() || fields[2].toByteArray(StandardCharsets.UTF_8).size > 255 ||
                fields[2].any { it == '|' || it.isISOControl() }) return
            id = fields[1]
            name = fields[2]
        }
        val ip = source.hostAddress ?: return
        if (id == null) {
            if (pcs.values.none { it.ip == ip && it.id != null }) pcs["ip:$ip"] = DiscoveredPc(ip, null, null)
        } else {
            pcs.remove("ip:$ip") // A legacy reply from the same address is not a second PC.
            pcs["id:$id"] = DiscoveredPc(ip, id, name)
        }
    }

    fun results(): List<DiscoveredPc> = pcs.values.sortedWith(compareBy({ it.name ?: "" }, { it.ip }))

    private fun isLocalAddress(address: Inet4Address): Boolean {
        val b = address.address.map { it.toInt() and 0xff }
        return b[0] == 10 || b[0] == 127 || b[0] == 192 && b[1] == 168 ||
            b[0] == 172 && b[1] in 16..31 || b[0] == 169 && b[1] == 254
    }
}
