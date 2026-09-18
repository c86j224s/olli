package dev.olli.mobile.ui.theme

import androidx.compose.foundation.isSystemInDarkTheme
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.darkColorScheme
import androidx.compose.material3.lightColorScheme
import androidx.compose.runtime.Composable
import androidx.compose.ui.graphics.Color

val OlliTeal = Color(0xFF1F8F83)
val OlliBlue = Color(0xFF476FC3)
val OlliGreen = Color(0xFF2F9B5D)
val OlliAmber = Color(0xFFD58B1C)
val OlliRed = Color(0xFFCA4538)

private val light = lightColorScheme(primary = OlliTeal, secondary = OlliBlue, tertiary = OlliAmber, error = OlliRed)
private val dark = darkColorScheme(primary = Color(0xFF62D6C7), secondary = Color(0xFF9BB7FF), tertiary = Color(0xFFFFC66D), error = Color(0xFFFF8A7F))

@Composable fun OlliMobileTheme(content: @Composable () -> Unit) {
    MaterialTheme(colorScheme = if (isSystemInDarkTheme()) dark else light, content = content)
}
