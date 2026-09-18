package dev.olli.mobile

import org.junit.Assert.assertEquals
import org.junit.Assert.assertThrows
import org.junit.Test

class ControllerClientTest {
    @Test fun normalizesHttpsControllerUrl() {
        assertEquals("https://controller.example", ControllerClient.normalizeBaseUrl("https://controller.example/"))
    }

    @Test fun rejectsCredentialsQueryAndPath() {
        listOf(
            "https://user:pass@controller.example",
            "https://controller.example/api",
            "https://controller.example?token=x",
            "file:///tmp/controller",
        ).forEach { value -> assertThrows(IllegalArgumentException::class.java) { ControllerClient.normalizeBaseUrl(value) } }
    }

    @Test fun debugAllowsOnlyEmulatorLoopbackHttp() {
        assertEquals("http://10.0.2.2:8766", ControllerClient.normalizeBaseUrl("http://10.0.2.2:8766"))
        assertThrows(IllegalArgumentException::class.java) { ControllerClient.normalizeBaseUrl("http://192.168.1.10:8766") }
    }
}
