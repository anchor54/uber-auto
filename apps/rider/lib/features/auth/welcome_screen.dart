import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/models.dart';
import '../../ui/map_preview.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import 'otp_screen.dart';

/// R1.1 Welcome: explain the idea and verify the phone.
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
      body: Column(
        children: [
          Expanded(
            child: SingleChildScrollView(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  WelcomeArt(height: MediaQuery.sizeOf(context).height < 700 ? 220 : 300),
                  Padding(
                    padding: const EdgeInsets.fromLTRB(20, 28, 20, 0),
                    child: Column(
                      crossAxisAlignment: CrossAxisAlignment.start,
                      children: [
                        Semantics(
                          header: true,
                          child: Text('Your daily commute, sorted', style: AppText.display(32, height: 1.1)),
                        ),
                        const SizedBox(height: 8),
                        Text(
                          "One regular driver, the same time every day, a fixed fare per ride. "
                          "Pay your driver directly. Skip any day you don't need.",
                          style: AppText.body(15, color: AppColors.muted, height: 1.5),
                        ),
                        const SizedBox(height: 18),
                        Text('Mobile number', style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
                        const SizedBox(height: 6),
                        Row(
                          children: [
                            Container(
                              height: 48,
                              padding: const EdgeInsets.symmetric(horizontal: 14),
                              decoration: BoxDecoration(
                                color: AppColors.surface,
                                border: Border.all(color: AppColors.line),
                                borderRadius: BorderRadius.circular(12),
                              ),
                              alignment: Alignment.center,
                              child: Text('+91', style: AppText.body(15, weight: FontWeight.w600)),
                            ),
                            const SizedBox(width: 8),
                            Expanded(
                              child: TextField(
                                key: const Key('welcome.phone'),
                                controller: _phone,
                                keyboardType: TextInputType.phone,
                                textInputAction: TextInputAction.done,
                                autofillHints: const [AutofillHints.telephoneNumberNational],
                                inputFormatters: [
                                  FilteringTextInputFormatter.digitsOnly,
                                  LengthLimitingTextInputFormatter(10),
                                ],
                                onSubmitted: (_) => _valid && !_sending ? _send() : null,
                                style: AppText.body(15, weight: FontWeight.w500),
                                decoration: _inputDecoration('10-digit number', error: _error != null),
                              ),
                            ),
                          ],
                        ),
                        if (_error != null) ...[
                          const SizedBox(height: 6),
                          Text(_error!, key: const Key('welcome.error'), style: AppText.body(13, color: AppColors.red)),
                        ],
                      ],
                    ),
                  ),
                ],
              ),
            ),
          ),
          BottomActions(
            padding: const EdgeInsets.fromLTRB(20, 16, 20, 16),
            children: [
              PrimaryButton(
                key: const Key('welcome.sendOtp'),
                label: 'Send OTP',
                loading: _sending,
                onPressed: _valid ? _send : null,
              ),
              const SizedBox(height: 6),
              LinkButton(
                key: const Key('welcome.driverSignup'),
                label: 'Want to drive? Sign up as a driver',
                onPressed: () => showInfo(context, 'Drivers sign up in the driver app, which comes after the rider flows.'),
              ),
            ],
          ),
        ],
      ),
    );
  }
}

InputDecoration _inputDecoration(String hint, {bool error = false}) {
  OutlineInputBorder border(Color c, [double w = 1]) => OutlineInputBorder(
        borderRadius: BorderRadius.circular(12),
        borderSide: BorderSide(color: c, width: w),
      );
  return InputDecoration(
    hintText: hint,
    hintStyle: AppText.body(15, color: AppColors.outline),
    filled: true,
    fillColor: AppColors.surface,
    isDense: true,
    contentPadding: const EdgeInsets.symmetric(horizontal: 14, vertical: 14),
    enabledBorder: border(error ? AppColors.red : AppColors.line),
    focusedBorder: border(error ? AppColors.red : AppColors.green, 2),
    border: border(AppColors.line),
  );
}

InputDecoration riderInputDecoration(String hint, {bool error = false}) => _inputDecoration(hint, error: error);
