import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/fake_api.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../home/home_screen.dart';
import 'welcome_screen.dart';

/// R1.1b Enter the code. Not drawn on the canvas; follows the Welcome style.
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

  String get _prettyPhone =>
      widget.phone.length == 10 ? '+91 ${widget.phone.substring(0, 5)} ${widget.phone.substring(5)}' : widget.phone;

  Future<void> _verify() async {
    setState(() => _verifying = true);
    final api = AppScope.of(context).api;
    try {
      await api.verifyOtp(widget.phone, _code.text);
      if (!mounted) return;
      Navigator.of(context).pushAndRemoveUntil(
        MaterialPageRoute<void>(builder: (_) => const HomeScreen()),
        (_) => false,
      );
    } on ApiException catch (e) {
      if (!mounted) return;
      _verifying = false;
      _code.clear();
      setState(() => _error = e.message);
    }
  }

  Future<void> _resend() async {
    await AppScope.of(context).api.sendOtp(widget.phone);
    if (mounted) showInfo(context, 'We sent a new code to $_prettyPhone');
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
                children: [
            ScreenTitle('Enter the code', subtitle: 'We sent a 6-digit code to $_prettyPhone.'),
            Padding(
              padding: const EdgeInsets.symmetric(horizontal: 20),
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  TextField(
                    key: const Key('otp.code'),
                    controller: _code,
                    autofocus: true,
                    keyboardType: TextInputType.number,
                    autofillHints: const [AutofillHints.oneTimeCode],
                    inputFormatters: [
                      FilteringTextInputFormatter.digitsOnly,
                      LengthLimitingTextInputFormatter(6),
                    ],
                    style: AppText.display(26, letterSpacing: 8),
                    textAlign: TextAlign.center,
                    decoration: riderInputDecoration('••••••', error: _error != null),
                  ),
                  const SizedBox(height: 8),
                  if (_error != null)
                    Text(_error!, key: const Key('otp.error'), style: AppText.body(13, color: AppColors.red))
                  else if (demo)
                    Text(
                      'Demo mode: use ${FakeRiderApi.demoOtp}',
                      key: const Key('otp.demoHint'),
                      style: AppText.body(13, color: AppColors.muted),
                    ),
                  const SizedBox(height: 4),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: LinkButton(key: const Key('otp.resend'), label: 'Resend code', onPressed: _resend),
                  ),
                ],
              ),
            ),
                ],
              ),
            ),
            BottomActions(
              padding: const EdgeInsets.fromLTRB(20, 12, 20, 16),
              children: [
                PrimaryButton(
                  key: const Key('otp.verify'),
                  label: 'Verify',
                  loading: _verifying,
                  onPressed: _code.text.length == 6 ? _verify : null,
                ),
              ],
            ),
          ],
        ),
      ),
    );
  }
}
