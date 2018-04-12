package ch.epfl.prifiproxy.activities;

import android.app.ActivityManager;
import android.app.AlertDialog;
import android.app.ProgressDialog;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.SharedPreferences;
import android.content.pm.PackageManager;
import android.net.Uri;
import android.os.AsyncTask;
import android.os.Bundle;
import android.os.HandlerThread;
import android.os.Process;
import android.support.v7.app.AppCompatActivity;
import android.util.Log;
import android.view.View;
import android.widget.Button;
import android.widget.ScrollView;
import android.widget.Switch;
import android.widget.TextView;
import android.widget.Toast;

import ch.epfl.prifiproxy.R;
import ch.epfl.prifiproxy.services.PrifiService;
import ch.epfl.prifiproxy.utils.NetworkStatusHelper;
import ch.epfl.prifiproxy.utils.OnScreenLogHandler;

import java.util.concurrent.atomic.AtomicBoolean;

import prifiMobile.PrifiMobile;

public class MainActivity extends AppCompatActivity {

    private final String ON_SCREEN_LOG_THREAD = "ON_SCREEN_LOG";
    private final String EMPTY_TEXT_VIEW = "";
    private final int DEFAULT_PING_TIMEOUT = 3000; // 3s

    private String prifiRelayAddress;
    private int prifiRelayPort;
    private int prifiRelaySocksPort;

    private AtomicBoolean isPrifiServiceRunning;
    private Button startButton, stopButton, testButton1, testButton2;
    private Switch logSwitch;
    private ScrollView mScrollView;
    private TextView onScreenLogTextView;
    private ProgressDialog mProgessDialog;

    private BroadcastReceiver mBroadcastReceiver;

    private HandlerThread mHandlerThread;
    private OnScreenLogHandler mOnScreenLogHandler;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);
        setContentView(R.layout.activity_main);

        // Load Variables from SharedPreferences
        SharedPreferences prifiPrefs = getSharedPreferences(getString(R.string.prifi_config_shared_preferences), MODE_PRIVATE);
        prifiRelayAddress = prifiPrefs.getString(getString(R.string.prifi_config_relay_address),"");
        prifiRelayPort = prifiPrefs.getInt(getString(R.string.prifi_config_relay_port), 0);
        prifiRelaySocksPort = prifiPrefs.getInt(getString(R.string.prifi_config_relay_socks_port),0);

        // Buttons
        startButton = findViewById(R.id.startButton);
        stopButton = findViewById(R.id.stopButton);
        testButton1 = findViewById(R.id.testButton1);
        testButton2 = findViewById(R.id.testButton2);

        // Other Views
        logSwitch = findViewById(R.id.logSwitch);
        mScrollView = findViewById(R.id.mainScrollView);
        onScreenLogTextView = findViewById(R.id.logTextView);

        mBroadcastReceiver = new BroadcastReceiver() {
            @Override
            public void onReceive(Context context, Intent intent) {
                String action = intent.getAction();

                switch (action) {
                    case PrifiService.PRIFI_STOPPED_BROADCAST_ACTION:
                        if (mProgessDialog.isShowing()) {
                            mProgessDialog.dismiss();
                        }
                        break;

                    case OnScreenLogHandler.UPDATE_LOG_BROADCAST_ACTION:
                        String log = intent.getExtras().getString(OnScreenLogHandler.UPDATE_LOG_INTENT_KEY);
                        updateOnScreenLog(log);
                        break;

                    default:
                        break;
                }

            }
        };

        mHandlerThread = new HandlerThread(ON_SCREEN_LOG_THREAD, Process.THREAD_PRIORITY_BACKGROUND);
        mHandlerThread.start();
        mOnScreenLogHandler = new OnScreenLogHandler(mHandlerThread.getLooper());

        startButton.setOnClickListener(view -> startPrifiService(this));

        stopButton.setOnClickListener(view -> stopPrifiService());

        logSwitch.setOnCheckedChangeListener((compoundButton, isChecked) -> {
            if (isChecked) {
                mOnScreenLogHandler.sendEmptyMessage(OnScreenLogHandler.UPDATE_LOG_MESSAGE_WHAT);
            } else {
                mOnScreenLogHandler.removeMessages(OnScreenLogHandler.UPDATE_LOG_MESSAGE_WHAT);
                updateOnScreenLog(EMPTY_TEXT_VIEW);
            }
        });

        testButton1.setOnClickListener(view -> {

        });

        testButton2.setOnClickListener(view -> {

        });
    }

    @Override
    protected void onResume() {
        super.onResume();

        isPrifiServiceRunning = new AtomicBoolean(isMyServiceRunning(PrifiService.class));

        IntentFilter intentFilter = new IntentFilter();
        intentFilter.addAction(PrifiService.PRIFI_STOPPED_BROADCAST_ACTION);
        intentFilter.addAction(OnScreenLogHandler.UPDATE_LOG_BROADCAST_ACTION);
        registerReceiver(mBroadcastReceiver, intentFilter);
    }

    @Override
    protected void onPause() {
        super.onPause();
        unregisterReceiver(mBroadcastReceiver);
    }

    @Override
    protected void onRestart() {
        super.onRestart();
        if (logSwitch.isChecked()) {
            mOnScreenLogHandler.sendEmptyMessage(OnScreenLogHandler.UPDATE_LOG_MESSAGE_WHAT);
        }
    }

    @Override
    protected void onStop() {
        super.onStop();
        mOnScreenLogHandler.removeMessages(OnScreenLogHandler.UPDATE_LOG_MESSAGE_WHAT);
    }

    private void updateOnScreenLog(String s) {
        onScreenLogTextView.setText(s);
        mScrollView.post(() -> mScrollView.fullScroll(View.FOCUS_DOWN));
    }

    private void startPrifiService(Context context) {
        if (!isPrifiServiceRunning.get()) {
            new AsyncTask<Void, Void, Boolean>() {
                @Override
                protected void onPreExecute() {
                    mProgessDialog = ProgressDialog.show(context, "Check Network Availability", "Please wait");
                }

                @Override
                protected Boolean doInBackground(Void... voids) {
                    // TODO get dynamic Server Address, SOCKS PORT and RELAY PORT
                    boolean isRelayAvailable = NetworkStatusHelper.isHostReachable(prifiRelayAddress, prifiRelayPort, DEFAULT_PING_TIMEOUT);
                    boolean isSocksAvailable = NetworkStatusHelper.isHostReachable(prifiRelayAddress, prifiRelaySocksPort, DEFAULT_PING_TIMEOUT);
                    return isRelayAvailable && isSocksAvailable;
                }

                @Override
                protected void onPostExecute(Boolean isNetworkAvailable) {
                    if (mProgessDialog.isShowing()) {mProgessDialog.dismiss();}
                    if (isNetworkAvailable) {
                        isPrifiServiceRunning.set(true);
                        startService(new Intent(context, PrifiService.class));
                        showRedirectDialog();
                    } else {
                        Toast.makeText(context, "Relay is not available", Toast.LENGTH_LONG).show();
                    }
                }
            }.execute();
        }
    }

    private void stopPrifiService() {
        if (isPrifiServiceRunning.compareAndSet(true, false)) {
            mProgessDialog = ProgressDialog.show(this, "Stopping PriFi", "Please wait");
            PrifiMobile.stopClient(); // StopClient will make the service to shutdown by itself
        }
    }

    private void showRedirectDialog() {
        AlertDialog alertDialog = new AlertDialog.Builder(this).create();
        alertDialog.setTitle("Open Telegram");
        alertDialog.setMessage("You will be redirected to Telegram");
        alertDialog.setButton(AlertDialog.BUTTON_NEGATIVE, "Cancel",
                (dialog, which) -> dialog.dismiss());
        alertDialog.setButton(AlertDialog.BUTTON_POSITIVE, "Go",
                (dialog, which) -> redirectToTelegram());
        alertDialog.show();
    }

    private void redirectToTelegram() {
        final String appName = "org.telegram.messenger";
        Intent intent;
        final boolean isAppInstalled = isAppAvailable(this, appName);
        if (isAppInstalled) {
            intent = getPackageManager().getLaunchIntentForPackage(appName);
        } else {
            intent = new Intent(Intent.ACTION_VIEW);
            intent.setData(Uri.parse("market://details?id=" + appName));
        }
        startActivity(intent);
    }

    private boolean isMyServiceRunning(Class<?> serviceClass) {
        ActivityManager manager = (ActivityManager) getSystemService(Context.ACTIVITY_SERVICE);
        for (ActivityManager.RunningServiceInfo service : manager.getRunningServices(Integer.MAX_VALUE)) {
            if (serviceClass.getName().equals(service.service.getClassName())) {
                return true;
            }
        }
        return false;
    }

    private boolean isAppAvailable(Context context, String appName) {
        PackageManager packageManager = context.getPackageManager();
        try {
            packageManager.getPackageInfo(appName, PackageManager.GET_ACTIVITIES);
            return true;
        } catch (PackageManager.NameNotFoundException e) {
            return false;
        }
    }

}
