package com.uberauto.rider

import android.app.NotificationChannel
import android.app.NotificationManager
import android.os.Build
import android.os.Bundle
import io.flutter.embedding.android.FlutterActivity

class MainActivity : FlutterActivity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        createRideUpdatesChannel()
    }

    /**
     * Ride alerts ("Ravi has arrived", …) are time-critical, so their channel
     * is high importance: Android shows them as a heads-up banner over the
     * screen, with sound, before they settle in the status bar. The backend
     * names this channel in every push, and the manifest makes it Firebase's
     * default. Creating an existing channel again changes nothing, so this is
     * safe on every start.
     */
    private fun createRideUpdatesChannel() {
        if (Build.VERSION.SDK_INT < Build.VERSION_CODES.O) return
        val channel = NotificationChannel(
            RIDE_UPDATES_CHANNEL,
            "Ride updates",
            NotificationManager.IMPORTANCE_HIGH,
        ).apply {
            description = "Your driver on the way, arriving, running late, and payment reminders"
        }
        getSystemService(NotificationManager::class.java).createNotificationChannel(channel)
    }

    companion object {
        const val RIDE_UPDATES_CHANNEL = "ride_updates"
    }
}
