# Create dedicated user
sudo useradd -r -s /bin/false -d /opt/leeforest leeforest

# Create directory structure
sudo mkdir -p /opt/leeforest/{www,certs,apps}
sudo chown -R leeforest:leeforest /opt/leeforest

# Install the service file
sudo cp leeforest.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable leeforest
sudo systemctl start leeforest