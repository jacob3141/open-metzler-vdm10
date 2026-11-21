package io.github.jacob3141.doorbell

import android.app.Activity
import android.app.Application
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.os.Bundle

class App : Application() {
    override fun onCreate() {
        super.onCreate()
        Notifications.createChannels(this)
        WifiNet.start(this)
        registerActivityLifecycleCallbacks(object : ActivityLifecycleCallbacks {
            override fun onActivityStarted(activity: Activity) {
                visible++
            }
            override fun onActivityStopped(activity: Activity) {
                visible--
            }
            override fun onActivityCreated(activity: Activity, savedInstanceState: Bundle?) {}
            override fun onActivityResumed(activity: Activity) {}
            override fun onActivityPaused(activity: Activity) {}
            override fun onActivitySaveInstanceState(activity: Activity, outState: Bundle) {}
            override fun onActivityDestroyed(activity: Activity) {}
        })
    }

    companion object {
        /** Number of started activities; above zero the app is on screen and may open the door view itself. */
        var visible = 0
            private set
    }
}

/** Starts the connection after a restart of the phone or an update of the app. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED || intent.action == Intent.ACTION_MY_PACKAGE_REPLACED) {
            DoorbellService.start(context)
        }
    }
}
