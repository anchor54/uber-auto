import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/fake_api.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../shell/driver_shell.dart';

/// Enter the code (a fixed code in dev builds and demo mode).
class OtpScreen extends StatefulWidget {
  const OtpScreen({super.key, required this.phone});

  final String phone;

  @override
  State<OtpScreen> createState() => _OtpScreenState();
}

class _OtpScreenState extends State<OtpScreen> {
  final _code = TextEditingController();
  bool _verifying = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _code.addListener(() {
      setState(() => _error = null);
      if (_code.text.length == 6 && !_verifying) _verify();
    });
  }

  @override
  void dispose() {
    _code.dispose();
    super.dispose();
  }

  Future<void> _verify() async {
    setState(() => _verifying = true);
    final api = AppScope.of(context).api;
    try {
      final profile = await api.verifyOtp(widget.phone, _code.text);
      if (!mounted) return;
      Navigator.of(context).pushAndRemoveUntil(
        MaterialPageRoute<void>(builder: (_) => DriverShell(profile: profile)),
        (_) => false,
      );
    } on ApiException catch (e) {
      if (!mounted) return;
      _verifying = false;
      _code.clear();
      setState(() => _error = e.message);
    }
  }

  @override
  Widget build(BuildContext context) {
    final demo = AppScope.of(context).demoMode;
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(),
            Expanded(
              child: ListView(
                padding: const EdgeInsets.symmetric(horizontal: 20),
                children: [
                  Text('Enter the code', style: AppText.display(28)),
                  const SizedBox(height: 6),
                  Text('We sent a 6-digit code to +91 ${widget.phone}.', style: AppText.body(14, color: AppColors.muted)),
                  const SizedBox(height: 16),
                  TextField(
                    key: const Key('otp.code'),
                    controller: _code,
                    autofocus: true,
                    keyboardType: TextInputType.number,
                    inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(6)],
                    style: AppText.display(26, letterSpacing: 8),
                    textAlign: TextAlign.center,
                    decoration: driverInputDecoration('••••••', error: _error != null),
                  ),
                  const SizedBox(height: 8),
                  if (_error != null)
                    Text(_error!, key: const Key('otp.error'), style: AppText.body(13, color: AppColors.red))
                  else if (demo)
                    Text('Demo mode: use ${FakeDriverApi.demoOtp}', key: const Key('otp.demoHint'), style: AppText.body(13, color: AppColors.muted)),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(
                key: const Key('otp.verify'),
                label: 'Verify',
                loading: _verifying,
                onPressed: _code.text.length == 6 ? _verify : null,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
