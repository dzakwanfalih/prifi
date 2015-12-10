rm *.log
rm *.out
rm config.sh
rm ./dissent/prifi
rm ./dissent/prifi-freebsd-amd64
rm ./dissent/*.sh
cp ./prifi/scripts-deterlab/* ./dissent/
cp ./prifi/scripts-deterlab/config.sh ~/config.sh
cp ./prifi/bin/prifi-linux-amd64/prifi ./dissent/prifi
cp ./prifi/bin/prifi-freebsd-amd64/prifi ./dissent/prifi-freebsd-amd64
chmod u+rwx ./dissent/*