import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'otp_screen.dart';

/// Driver sign-in: phone number, then the code. Drivers are onboarded by
/// ops (D1), so only a registered driver's number gets a code.
class WelcomeScreen extends StatefulWidget {
  const WelcomeScreen({super.key});

  @override
  State<WelcomeScreen> createState() => _WelcomeScreenState();
}

class _WelcomeScreenState extends State<WelcomeScreen> {
  final _phone = TextEditingController();
  bool _sending = false;
  String? _error;

  @override
  void initState() {
    super.initState();
    _phone.addListener(() => setState(() => _error = null));
  }

  @override
  void dispose() {
    _phone.dispose();
    super.dispose();
  }

  bool get _valid => _phone.text.length == 10;

  Future<void> _send() async {
    if (!RegExp(r'^[6-9]').hasMatch(_phone.text)) {
      setState(() => _error = 'Enter a 10-digit mobile number starting with 6, 7, 8 or 9');
      return;
    }
    setState(() => _sending = true);
    final api = AppScope.of(context).api;
    try {
      await api.sendOtp(_phone.text);
      if (!mounted) return;
      setState(() => _sending = false);
      await Navigator.of(context).push(MaterialPageRoute<void>(builder: (_) => OtpScreen(phone: _phone.text)));
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _sending = false;
        _error = e.message;
      });
    }
  }

  @override
  Widget build(BuildContext context) {
    return Scaffold(
      body: SafeArea(
        child: Column(
          children: [
            Expanded(
              child: ListView(
                padding: const EdgeInsets.fromLTRB(20, 48, 20, 16),
                children: [
                  Container(
                    width: 56,
                    height: 56,
                    decoration: BoxDecoration(color: AppColors.yellow, borderRadius: BorderRadius.circular(16)),
                    child: const Icon(Icons.directions_car_filled, color: AppColors.onYellow, size: 30),
                  ),
                  const SizedBox(height: 24),
                  Semantics(header: true, child: Text('Drive regular riders', style: AppText.display(32, height: 1.1))),
                  const SizedBox(height: 8),
                  Text(
                    'The same riders at the same times, every week. A flat monthly plan, no commission. '
                    'Riders pay you directly.',
                    style: AppText.body(15, color: AppColors.muted, height: 1.5),
                  ),
                  const SizedBox(height: 24),
                  Text('Mobile number', style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
                  const SizedBox(height: 6),
                  TextField(
                    key: const Key('welcome.phone'),
                    controller: _phone,
                    keyboardType: TextInputType.phone,
                    inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(10)],
                    onSubmitted: (_) => _valid && !_sending ? _send() : null,
                    style: AppText.body(16, weight: FontWeight.w500),
                    decoration: driverInputDecoration('10-digit number', error: _error != null).copyWith(prefixText: '+91  '),
                  ),
                  if (_error != null) ...[
                    const SizedBox(height: 6),
                    Text(_error!, key: const Key('welcome.error'), style: AppText.body(13, color: AppColors.red)),
                  ],
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(
                key: const Key('welcome.sendOtp'),
                label: 'Send OTP',
                loading: _sending,
                onPressed: _valid ? _send : null,
              ),
            ),
          ],
        ),
      ),
    );
  }
}
