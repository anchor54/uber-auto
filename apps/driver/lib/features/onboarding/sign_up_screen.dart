import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../../app_scope.dart';
import '../../core/api.dart';
import '../../core/models.dart';
import '../../ui/tokens.dart';
import '../../ui/widgets.dart';
import '../auth/welcome_screen.dart';
import 'documents_screen.dart';
import 'onboarding_common.dart';

/// D1.1 Sign up: name as on the licence, mobile number, city, the terms,
/// then the code. A number that already has a driver profile goes where
/// its status says.
///
/// With [verifiedPhone] the number was verified already (signed in on
/// Welcome with a new number): the screen asks only for the rest.
class SignUpScreen extends StatefulWidget {
  const SignUpScreen({super.key, this.verifiedPhone});

  final String? verifiedPhone;

  @override
  State<SignUpScreen> createState() => _SignUpScreenState();
}

class _SignUpScreenState extends State<SignUpScreen> {
  final _name = TextEditingController();
  final _phone = TextEditingController();
  final _code = TextEditingController();
  String _city = driverCities.first.$1;
  bool _terms = false;
  bool _codeSent = false;
  bool _busy = false;
  String? _error;
  String? _codeError;

  bool get _verified => widget.verifiedPhone != null;

  @override
  void initState() {
    super.initState();
    if (_verified) _phone.text = widget.verifiedPhone!;
    _name.addListener(_changed);
    _phone.addListener(() {
      // A new number needs a new code.
      if (_codeSent && !_verified) {
        _codeSent = false;
        _code.clear();
      }
      _changed();
    });
    _code.addListener(() {
      _codeError = null;
      _changed();
    });
  }

  void _changed() {
    if (mounted) setState(() => _error = null);
  }

  @override
  void dispose() {
    _name.dispose();
    _phone.dispose();
    _code.dispose();
    super.dispose();
  }

  bool get _formReady => _name.text.trim().length >= 2 && _phone.text.length == 10 && _terms;

  Future<void> _getCode() async {
    if (!RegExp(r'^[6-9]').hasMatch(_phone.text)) {
      setState(() => _error = 'Enter a 10-digit mobile number starting with 6, 7, 8 or 9');
      return;
    }
    setState(() => _busy = true);
    try {
      await AppScope.of(context).api.sendOtp(_phone.text);
      if (!mounted) return;
      setState(() {
        _busy = false;
        _codeSent = true;
      });
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = e.message;
      });
    }
  }

  Future<void> _verify() async {
    setState(() => _busy = true);
    final scope = AppScope.of(context);
    try {
      final profile = await scope.api.verifyOtp(_phone.text, _code.text);
      if (profile.driverExists) {
        // Already registered: carry on where they are.
        final home = await driverHome(scope.api, scope.clock.now(), signedIn: profile);
        if (mounted) openAsRoot(context, home);
        return;
      }
      await _register(scope.api);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        if (e.code == 'invalid_code' || e.code == 'wrong_code' || e.code == 'code_expired') {
          _codeError = e.code == 'code_expired' ? e.message : "That code didn't match";
          _code.clear();
        } else {
          _error = e.message;
        }
      });
    }
  }

  /// driver.register, then D1.2. Throws [ApiException] for the caller to show.
  Future<void> _register(DriverApi api) async {
    await api.driverSignup(name: _name.text.trim(), city: _city, termsAccepted: _terms);
    if (mounted) openAsRoot(context, const DocumentsScreen());
  }

  Future<void> _continueVerified() async {
    setState(() => _busy = true);
    try {
      await _register(AppScope.of(context).api);
    } on ApiException catch (e) {
      if (!mounted) return;
      setState(() {
        _busy = false;
        _error = e.message;
      });
    }
  }

  void _logIn() {
    final nav = Navigator.of(context);
    if (nav.canPop()) {
      nav.pop();
    } else {
      nav.pushReplacement(MaterialPageRoute<void>(builder: (_) => const WelcomeScreen()));
    }
  }

  void _showTerms() {
    showModalBottomSheet<void>(
      context: context,
      backgroundColor: AppColors.card,
      showDragHandle: true,
      isScrollControlled: true,
      builder: (sheet) => SafeArea(
        child: Padding(
          key: const Key('signup.termsSheet'),
          padding: const EdgeInsets.fromLTRB(20, 0, 20, 20),
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.start,
            children: [
              Text('Driver terms', style: AppText.display(22)),
              const SizedBox(height: 12),
              for (final t in const [
                'You drive your own vehicle and hold a valid commercial licence, permit and insurance.',
                'You pay one flat monthly fee. Riders pay you directly; we take no commission.',
                'We check your documents and may run a background check before you go live.',
                'Your documents are used only to verify you and are never shown to riders.',
                'Rides you accept are a commitment: tell us early if you can\'t make one.',
              ])
                Padding(
                  padding: const EdgeInsets.only(bottom: 10),
                  child: Text('• $t', style: AppText.body(14, color: AppColors.muted, height: 1.45)),
                ),
              const SizedBox(height: 8),
              PrimaryButton(label: 'Close', height: 48, onPressed: () => Navigator.of(sheet).pop()),
            ],
          ),
        ),
      ),
    );
  }

  @override
  Widget build(BuildContext context) {
    final VoidCallback? action;
    final String label;
    if (_verified) {
      label = 'Continue';
      action = _formReady ? _continueVerified : null;
    } else if (_codeSent) {
      label = 'Verify';
      action = _code.text.length == 6 ? _verify : null;
    } else {
      label = 'Get code';
      action = _formReady ? _getCode : null;
    }
    final demo = AppScope.of(context).demoMode;
    return Scaffold(
      body: SafeArea(
        child: Column(
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const FlowHeader(label: 'Drive with us · Step 1 of 5'),
            Expanded(
              child: ListView(
                key: const Key('signup.list'),
                padding: const EdgeInsets.fromLTRB(20, 4, 20, 16),
                children: [
                  Semantics(header: true, child: Text('Drive the same riders, every day', style: AppText.display(28))),
                  const SizedBox(height: 14),
                  for (final b in const [
                    'A fixed weekly schedule you agree to',
                    'Keep every fare, pay one monthly fee',
                    'Riders pay you directly, UPI or cash',
                  ])
                    Padding(
                      padding: const EdgeInsets.only(bottom: 8),
                      child: Row(
                        children: [
                          const Icon(Icons.check_circle, size: 18, color: AppColors.green),
                          const SizedBox(width: 10),
                          Expanded(child: Text(b, style: AppText.body(15))),
                        ],
                      ),
                    ),
                  const SizedBox(height: 14),
                  LabelledField(
                    label: 'Full name as on your licence',
                    controller: _name,
                    hint: 'e.g. Ravi Kumar',
                    textCapitalization: TextCapitalization.words,
                  ),
                  const SizedBox(height: 14),
                  LabelledField(
                    label: 'Mobile number',
                    controller: _phone,
                    hint: '10-digit number',
                    prefixText: '+91  ',
                    enabled: !_verified,
                    keyboardType: TextInputType.phone,
                    inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(10)],
                  ),
                  const SizedBox(height: 14),
                  Text("City you'll drive in", style: AppText.body(13, weight: FontWeight.w600, color: AppColors.muted)),
                  const SizedBox(height: 6),
                  Wrap(
                    spacing: 8,
                    children: [
                      for (final (code, name) in driverCities)
                        ChoiceChip(
                          key: labelKey(name),
                          label: Text(name),
                          selected: _city == code,
                          onSelected: (_) => setState(() => _city = code),
                        ),
                    ],
                  ),
                  const SizedBox(height: 10),
                  Row(
                    children: [
                      Checkbox(
                        key: const Key('terms'),
                        value: _terms,
                        activeColor: AppColors.yellow,
                        checkColor: AppColors.onYellow,
                        onChanged: (v) => setState(() => _terms = v ?? false),
                      ),
                      Expanded(
                        child: GestureDetector(
                          onTap: () => setState(() => _terms = !_terms),
                          child: Text('I agree to the driver terms and to a background check.',
                              style: AppText.body(14, height: 1.4)),
                        ),
                      ),
                    ],
                  ),
                  Align(
                    alignment: Alignment.centerLeft,
                    child: LinkButton(label: 'Read the driver terms', onPressed: _showTerms),
                  ),
                  if (_codeSent && !_verified) ...[
                    const SizedBox(height: 10),
                    Text('We sent a 6-digit code to +91 ${_phone.text}.', style: AppText.body(14, color: AppColors.muted)),
                    const SizedBox(height: 8),
                    TextField(
                      key: const Key('signup.code'),
                      controller: _code,
                      autofocus: true,
                      keyboardType: TextInputType.number,
                      inputFormatters: [FilteringTextInputFormatter.digitsOnly, LengthLimitingTextInputFormatter(6)],
                      style: AppText.display(24, letterSpacing: 8),
                      textAlign: TextAlign.center,
                      decoration: driverInputDecoration('6-digit code', error: _codeError != null),
                    ),
                    const SizedBox(height: 6),
                    if (_codeError != null)
                      Text(_codeError!, key: const Key('signup.codeError'), style: AppText.body(13, color: AppColors.orange))
                    else if (demo)
                      Text('Demo mode: use 123456', style: AppText.body(13, color: AppColors.muted)),
                  ],
                  if (_error != null) ...[
                    const SizedBox(height: 10),
                    Text(_error!, key: const Key('signup.error'), style: AppText.body(14, color: AppColors.orange, height: 1.4)),
                  ],
                  const SizedBox(height: 8),
                  Center(child: LinkButton(label: 'Already registered? Log in', onPressed: _logIn)),
                ],
              ),
            ),
            Padding(
              padding: const EdgeInsets.fromLTRB(20, 8, 20, 16),
              child: PrimaryButton(label: label, height: 52, loading: _busy, onPressed: action),
            ),
          ],
        ),
      ),
    );
  }
}
