package dev.rocket.android;

import java.net.InetAddress;
import java.nio.charset.StandardCharsets;
import java.util.List;

// Dependency-free JVM smoke test for the Android discovery parser; run via run-smoke.ps1.
public final class PcDiscoverySmoke {
    private static void reply(PcDiscovery discovery, String source, int port, byte[] bytes) throws Exception {
        discovery.add(InetAddress.getByName(source), port, bytes, bytes.length);
    }

    private static byte[] utf8(String text) { return text.getBytes(StandardCharsets.UTF_8); }

    private static void check(boolean condition, String message) {
        if (!condition) throw new AssertionError(message);
    }

    public static void main(String[] args) throws Exception {
        String id = "0123456789abcdef0123456789abcdef";
        PcDiscovery discovery = new PcDiscovery();
        reply(discovery, "192.168.1.2", 42069, utf8("ROCKET_HERE_V1"));
        reply(discovery, "192.168.1.2", 42069, utf8("ROCKET_HERE_V1|" + id + "|Desk PC"));
        reply(discovery, "192.168.1.3", 42069, utf8("ROCKET_HERE_V1|" + id + "|Desk PC"));
        reply(discovery, "10.0.0.4", 42069, utf8("ROCKET_HERE_V1"));
        reply(discovery, "10.0.0.4", 42069, utf8("ROCKET_HERE_V1"));
        List<DiscoveredPc> found = discovery.results();
        check(found.size() == 2, "deduplicate modern ID and legacy IP");
        check(found.stream().anyMatch(pc -> id.equals(pc.getId()) && "192.168.1.3".equals(pc.getIp())),
            "keep latest address for an ID");
        check(found.stream().anyMatch(pc -> pc.getId() == null && "10.0.0.4".equals(pc.getIp())),
            "keep legacy responder");

        String secondId = "fedcba9876543210fedcba9876543210";
        reply(discovery, "192.168.1.4", 42069, utf8("ROCKET_HERE_V1|" + secondId + "|Second PC"));
        check(discovery.results().size() == 3, "both modern PCs remain individually selectable");

        reply(discovery, "8.8.8.8", 42069, utf8("ROCKET_HERE_V1"));
        reply(discovery, "10.0.0.5", 1234, utf8("ROCKET_HERE_V1"));
        reply(discovery, "10.0.0.5", 42069, utf8("ROCKET_HERE_V1|UPPERCASEID|PC"));
        reply(discovery, "10.0.0.5", 42069, utf8("ROCKET_HERE_V1|" + id + "|bad\nname"));
        reply(discovery, "10.0.0.5", 42069, new byte[]{(byte) 0xc3, (byte) 0x28});
        check(discovery.results().size() == 3, "ignore non-local, wrong-port and malformed replies");
        System.out.println("PcDiscoverySmoke passed");
    }
}
